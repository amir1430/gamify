package app

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/realtime"
)

func TestLoopRuns(t *testing.T) {
	a := &App{
		Games:    gameconfig.NewService(nil, nil),
		Realtime: blockingTransport{},
		Router:   realtime.NewRouter(),
		HTTP:     http.NewServeMux(),
		cfg:      Config{HTTPAddr: freeAddr(t), MetricsAddr: "off"},
	}
	var n atomic.Int64
	a.Loop(10*time.Millisecond, func(context.Context) { n.Add(1) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got < 3 {
		t.Fatalf("loop ran %d times, want >= 3", got)
	}
}
