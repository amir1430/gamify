// Package realtime carries messages between Go and the clients. A Transport (Centrifugo) moves
// them; the Router dispatches client RPCs to game handlers whatever the transport.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/amir1430/gamify/metrics"
)

// Transport is a realtime driver. Only Go publishes; clients send RPCs, never publications.
type Transport interface {
	// Publish sends data to every subscriber of channel.
	Publish(ctx context.Context, channel string, data []byte) error
	// Disconnect closes every connection of a user for good (a suspended player): the client
	// must not reconnect.
	Disconnect(ctx context.Context, userID int64) error
	// Online is the open connections of every connected user.
	Online(ctx context.Context) (map[int64]int, error)
	// Token is the credential a logged-in user's client connects with. name is what other
	// players see of them in presence.
	Token(userID int64, name string, now time.Time) string
	// Serve delivers client RPCs to r until ctx ends.
	Serve(ctx context.Context, r *Router) error
	// Ping checks the transport answers.
	Ping(ctx context.Context) error
}

// OnlineChannel is joined by every connection and only read for its presence (Online).
const OnlineChannel = "online:all"

// UserChannel is the personal channel Go publishes balance and game updates to.
func UserChannel(userID int64) string { return "user:" + strconv.FormatInt(userID, 10) }

// Handler serves one RPC method. userID comes from the transport's authenticated connection,
// never from the payload.
type Handler func(ctx context.Context, userID int64, data []byte) ([]byte, error)

// JSON adapts a typed handler: parses the JSON payload, encodes the JSON result.
func JSON[Req, Resp any](f func(ctx context.Context, userID int64, req Req) (Resp, error)) Handler {
	return func(ctx context.Context, userID int64, data []byte) ([]byte, error) {
		var req Req
		if len(data) > 0 {
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, &ClientError{Code: CodeBadPayload, Message: "bad payload"}
			}
		}
		resp, err := f(ctx, userID, req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(resp)
	}
}

// Error codes. Games use 1100 and up; transports keep their own below 1000 (Centrifugo: < 400).
const (
	CodeInternal        = 1000
	CodeUnknownMethod   = 1001
	CodeUnauthenticated = 1002
	CodeBadPayload      = 1003
)

// ClientError is returned to the client as-is. Any other error becomes a generic internal error.
type ClientError struct {
	Code    uint32
	Message string
}

func (e *ClientError) Error() string { return e.Message }

// Router maps RPC methods ("dice.bet") to handlers.
type Router struct {
	methods map[string]Handler
	// Check, when set, runs before every method: an error refuses the call (a restricted player).
	Check func(ctx context.Context, userID int64, method string) error
}

func NewRouter() *Router { return &Router{methods: map[string]Handler{}} }

// Handle registers a method; registering one twice is a wiring bug and panics.
func (r *Router) Handle(method string, h Handler) {
	if _, ok := r.methods[method]; ok {
		panic("realtime: method " + method + " registered twice")
	}
	r.methods[method] = h
}

// Methods lists the registered methods, sorted.
func (r *Router) Methods() []string {
	out := make([]string, 0, len(r.methods))
	for m := range r.methods {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

// Call serves one RPC for user (the transport's authenticated user ID, "" = none) and returns
// the result or the error for the client.
func (r *Router) Call(ctx context.Context, method, user string, data []byte) ([]byte, *ClientError) {
	h, ok := r.methods[method]
	if !ok {
		return nil, &ClientError{Code: CodeUnknownMethod, Message: "unknown method"} // not recorded: the name is the client's
	}
	start := time.Now()
	out, cerr := r.call(ctx, method, user, data, h)
	var code uint32
	if cerr != nil {
		code = cerr.Code
	}
	metrics.RPC(ctx, method, user, code, time.Since(start))
	return out, cerr
}

func (r *Router) call(ctx context.Context, method, user string, data []byte, h Handler) ([]byte, *ClientError) {
	userID, err := strconv.ParseInt(user, 10, 64)
	if user == "" || err != nil {
		return nil, &ClientError{Code: CodeUnauthenticated, Message: "unauthenticated"}
	}
	if r.Check != nil {
		err = r.Check(ctx, userID, method)
	}
	var out []byte
	if err == nil {
		out, err = h(ctx, userID, data)
	}
	if err != nil {
		if ce, ok := errors.AsType[*ClientError](err); ok {
			return nil, ce
		}
		slog.ErrorContext(ctx, "rpc failed", "method", method, "user", user, "err", err)
		return nil, &ClientError{Code: CodeInternal, Message: "internal error"}
	}
	return out, nil
}

// PublishJSON publishes v as JSON.
func PublishJSON(ctx context.Context, t Transport, channel string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("realtime: encode for %s: %w", channel, err)
	}
	return t.Publish(ctx, channel, data)
}
