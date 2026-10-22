package app

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/realtime"
)

func TestBetsRegistry(t *testing.T) {
	a := &App{}
	a.Bets("arena", func(context.Context, int64, int64, int32) ([]Bet, error) { return nil, nil })
	got := a.BetSources()
	if got["arena"] == nil {
		t.Fatal(`BetSources() has no "arena"`)
	}
	delete(got, "arena")
	got["x"] = nil
	if again := a.BetSources(); again["arena"] == nil || len(again) != 1 {
		t.Fatalf("mutating the copy changed the registry: %v", again)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate game did not panic")
		}
	}()
	a.Bets("arena", nil)
}

// blockingTransport serves until ctx ends, like a real transport.
type blockingTransport struct{ fakeTransport }

func (blockingTransport) Serve(ctx context.Context, _ *realtime.Router) error {
	<-ctx.Done()
	return nil
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestServeRunsAndStops(t *testing.T) {
	extra := freeAddr(t)
	a := &App{
		Games:    gameconfig.NewService(nil, nil),
		Realtime: blockingTransport{},
		Router:   realtime.NewRouter(),
		HTTP:     http.NewServeMux(),
		cfg:      Config{HTTPAddr: freeAddr(t), MetricsAddr: "off"},
	}
	a.Serve("extra", &http.Server{Addr: extra, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	var status int
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get("http://" + extra)
		if err == nil {
			resp.Body.Close()
			status = resp.StatusCode
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("extra server never answered: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
