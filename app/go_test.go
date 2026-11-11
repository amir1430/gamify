package app

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/realtime"
)

func TestGoRestarts(t *testing.T) {
	old := goBackoff
	goBackoff = 5 * time.Millisecond
	defer func() { goBackoff = old }()
	a := &App{
		Games:    gameconfig.NewService(nil, nil),
		Realtime: blockingTransport{},
		Router:   realtime.NewRouter(),
		HTTP:     http.NewServeMux(),
		cfg:      Config{HTTPAddr: freeAddr(t), MetricsAddr: "off"},
	}
	var runs atomic.Int64
	stopped := make(chan struct{})
	a.Go("w", func(ctx context.Context) error {
		if runs.Add(1) == 1 {
			return errors.New("boom")
		}
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(200 * time.Millisecond)
	if got := runs.Load(); got != 2 {
		t.Fatalf("ran %d times, want 2", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop on shutdown")
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("restarted after shutdown: %d runs", got)
	}
}
