// Package centrifugo is the Centrifugo (v6) realtime driver: Go publishes over Centrifugo's gRPC
// server API, and Centrifugo forwards client RPCs to Go over its gRPC RPC proxy (Serve).
// Regenerate apiproto / proxyproto from proto/centrifugo only when bumping Centrifugo.
package centrifugo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/amir1430/gamify/realtime"
	"github.com/amir1430/gamify/realtime/centrifugo/apiproto"
	"github.com/amir1430/gamify/realtime/centrifugo/proxyproto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	APIAddr     string        // Centrifugo's gRPC server API (grpc_api.port), e.g. centrifugo:10000
	APIKey      string        // its grpc_api.key
	ProxyAddr   string        // where Serve listens for Centrifugo's RPC proxy, e.g. :10001
	TokenSecret []byte        // client.token.hmac_secret_key: signs connection tokens
	TokenTTL    time.Duration // connection token lifetime (default 1h)
}

// SuspendedCode closes a suspended player's connections; 3500…3999 tell the client not to reconnect.
const SuspendedCode = 3501

type Transport struct {
	cfg  Config
	conn *grpc.ClientConn
	api  apiproto.CentrifugoApiClient
}

var _ realtime.Transport = (*Transport)(nil)

func New(cfg Config) (*Transport, error) {
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = time.Hour
	}
	if len(cfg.TokenSecret) == 0 {
		return nil, fmt.Errorf("centrifugo: no token secret")
	}
	// ponytail: plaintext gRPC, fine on a private network; add TLS if Centrifugo leaves it.
	conn, err := grpc.NewClient(cfg.APIAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(apiKeyCreds("apikey "+cfg.APIKey)),
	)
	if err != nil {
		return nil, err
	}
	return &Transport{cfg: cfg, conn: conn, api: apiproto.NewCentrifugoApiClient(conn)}, nil
}

func (t *Transport) Close() error { return t.conn.Close() }

func (t *Transport) Publish(ctx context.Context, channel string, data []byte) error {
	resp, err := t.api.Publish(ctx, &apiproto.PublishRequest{Channel: channel, Data: data})
	if err != nil {
		return err
	}
	if e := resp.GetError(); e != nil {
		return fmt.Errorf("centrifugo publish: %d %s", e.Code, e.Message)
	}
	return nil
}

var _ realtime.Broadcaster = (*Transport)(nil)

func (t *Transport) Broadcast(ctx context.Context, channels []string, data []byte) error {
	resp, err := t.api.Broadcast(ctx, &apiproto.BroadcastRequest{Channels: channels, Data: data})
	if err != nil {
		return err
	}
	if e := resp.GetError(); e != nil {
		return fmt.Errorf("centrifugo broadcast: %d %s", e.Code, e.Message)
	}
	return broadcastErr(channels, resp.GetResult().GetResponses())
}

// broadcastErr joins the per-channel errors of a broadcast (responses are in channels' order).
func broadcastErr(channels []string, responses []*apiproto.PublishResponse) error {
	var errs []error
	for i, r := range responses {
		if e := r.GetError(); e != nil && i < len(channels) {
			errs = append(errs, fmt.Errorf("centrifugo broadcast %s: %d %s", channels[i], e.Code, e.Message))
		}
	}
	return errors.Join(errs...)
}

func (t *Transport) Disconnect(ctx context.Context, userID int64) error {
	resp, err := t.api.Disconnect(ctx, &apiproto.DisconnectRequest{User: strconv.FormatInt(userID, 10),
		Disconnect: &apiproto.Disconnect{Code: SuspendedCode, Reason: "suspended"}})
	if err != nil {
		return err
	}
	if e := resp.GetError(); e != nil {
		return fmt.Errorf("centrifugo disconnect: %d %s", e.Code, e.Message)
	}
	return nil
}

// Online reads the presence of realtime.OnlineChannel, which every connection joins (server-side,
// from its token).
func (t *Transport) Online(ctx context.Context) (map[int64]int, error) {
	resp, err := t.api.Presence(ctx, &apiproto.PresenceRequest{Channel: realtime.OnlineChannel})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, fmt.Errorf("centrifugo presence: %d %s", e.Code, e.Message)
	}
	out := map[int64]int{}
	for _, c := range resp.GetResult().GetPresence() {
		if id, err := strconv.ParseInt(c.GetUser(), 10, 64); err == nil {
			out[id]++
		}
	}
	return out, nil
}

// Ping checks Centrifugo answers the server API with our key.
func (t *Transport) Ping(ctx context.Context) error {
	resp, err := t.api.Info(ctx, &apiproto.InfoRequest{})
	if err != nil {
		return err
	}
	if e := resp.GetError(); e != nil {
		return fmt.Errorf("centrifugo info: %d %s", e.Code, e.Message)
	}
	return nil
}

// Token is an HS256 connection JWT, server-side subscribed to the user's channel and the online channel.
func (t *Transport) Token(userID int64, name string, now time.Time) string {
	claims, _ := json.Marshal(map[string]any{
		"sub":      strconv.FormatInt(userID, 10),
		"exp":      now.Add(t.cfg.TokenTTL).Unix(),
		"channels": []string{realtime.UserChannel(userID), realtime.OnlineChannel},
		"info":     map[string]string{"name": name},
	})
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	m := hmac.New(sha256.New, t.cfg.TokenSecret)
	m.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(m.Sum(nil))
}

// Serve runs the gRPC RPC proxy on ProxyAddr until ctx ends.
func (t *Transport) Serve(ctx context.Context, r *realtime.Router) error {
	lis, err := net.Listen("tcp", t.cfg.ProxyAddr)
	if err != nil {
		return err
	}
	srv := grpc.NewServer()
	proxyproto.RegisterCentrifugoProxyServer(srv, &proxy{r: r})
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	return srv.Serve(lis)
}

// proxy implements Centrifugo's gRPC proxy; only RPC is used.
type proxy struct {
	proxyproto.UnimplementedCentrifugoProxyServer
	r *realtime.Router
}

func (p *proxy) RPC(ctx context.Context, req *proxyproto.RPCRequest) (*proxyproto.RPCResponse, error) {
	out, cerr := p.r.Call(ctx, req.Method, req.User, req.Data)
	if cerr != nil {
		return &proxyproto.RPCResponse{Error: &proxyproto.Error{Code: cerr.Code, Message: cerr.Message}}, nil
	}
	return &proxyproto.RPCResponse{Result: &proxyproto.RPCResult{Data: out}}, nil
}

type apiKeyCreds string

func (c apiKeyCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": string(c)}, nil
}

func (apiKeyCreds) RequireTransportSecurity() bool { return false }
