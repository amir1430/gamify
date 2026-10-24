package realtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amir1430/gamify/realtime"
)

// publisher is a Transport without Broadcast: Broadcast falls back to one Publish per channel.
type publisher struct {
	realtime.Transport
	sent []string
	fail string
}

func (p *publisher) Publish(_ context.Context, channel string, data []byte) error {
	if channel == p.fail {
		return errors.New("down")
	}
	p.sent = append(p.sent, channel+"="+string(data))
	return nil
}

type broadcaster struct {
	publisher
	calls int
}

func (b *broadcaster) Broadcast(_ context.Context, channels []string, data []byte) error {
	b.calls++
	return nil
}

func TestBroadcast(t *testing.T) {
	ctx := context.Background()
	p := &publisher{fail: "user:2"}
	err := realtime.Broadcast(ctx, p, []string{"user:1", "user:2", "user:3"}, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "user:2") {
		t.Fatalf("err %v", err)
	}
	if strings.Join(p.sent, ",") != "user:1=x,user:3=x" {
		t.Fatalf("sent %v", p.sent)
	}

	b := &broadcaster{}
	if err := realtime.Broadcast(ctx, b, []string{"user:1", "user:2"}, []byte("x")); err != nil || b.calls != 1 || len(b.sent) != 0 {
		t.Fatalf("err %v, calls %d, published %v", err, b.calls, b.sent)
	}
	if err := realtime.Broadcast(ctx, b, nil, []byte("x")); err != nil || b.calls != 1 {
		t.Fatalf("no channels: err %v, calls %d", err, b.calls)
	}
}
