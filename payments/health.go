package payments

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/amir1430/gamify/ledger"
)

// maxErrLen bounds a method's last error as Health reports it.
const maxErrLen = 300

// MethodHealth is how one method has done since this process started (nothing is stored: a restart
// clears it). A deposit method is its Receiver: whether Run is going, how often it returned, and
// when Received last recorded a payment. A withdrawal method is its Sender: when Send or Status last
// answered, and the last error either returned.
type MethodHealth struct {
	Name        string
	Kind        string            // "deposit" or "withdraw"
	Currencies  []ledger.Currency // sorted
	Running     bool              // deposit: Run is going
	Restarts    int               // deposit: how often Run returned
	LastOK      time.Time
	LastError   string
	LastErrorAt time.Time
}

type healthKey struct{ name, kind string }

type health struct {
	running     bool
	restarts    int
	lastOK      time.Time
	lastErr     string
	lastErrorAt time.Time
}

// note records a call's outcome for the method (s.mu must not be held).
func (s *Service) note(name, kind string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.healthOf(name, kind)
	if err == nil {
		h.lastOK = time.Now()
		return
	}
	msg := err.Error()
	if len(msg) > maxErrLen {
		msg = msg[:maxErrLen] + "…"
	}
	h.lastErr, h.lastErrorAt = msg, time.Now()
}

// healthOf is the method's record, made on first use (s.mu held).
func (s *Service) healthOf(name, kind string) *health {
	k := healthKey{name, kind}
	h := s.health[k]
	if h == nil {
		h = &health{}
		s.health[k] = h
	}
	return h
}

// runReceiver runs the named Receiver's Run once, recording that it runs and how it ended.
func (s *Service) runReceiver(ctx context.Context, name string) error {
	s.mu.Lock()
	r := s.running[name]
	s.healthOf(name, "deposit").running = true
	s.mu.Unlock()
	err := r.Run(ctx, &Host{Mux: s.a.HTTP, Method: name, s: s})
	if ctx.Err() != nil { // shutdown, not a failure
		s.mu.Lock()
		s.healthOf(name, "deposit").running = false
		s.mu.Unlock()
		return err
	}
	if err == nil {
		err = errors.New("Run returned")
	}
	s.note(name, "deposit", err)
	s.mu.Lock()
	h := s.healthOf(name, "deposit")
	h.running = false
	h.restarts++
	s.mu.Unlock()
	return err
}

// Health lists every registered method's health, by name, then kind.
func (s *Service) Health() []MethodHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := map[healthKey][]ledger.Currency{}
	for k := range s.receivers {
		hk := healthKey{k.method, "deposit"}
		cur[hk] = append(cur[hk], k.c)
	}
	for k := range s.senders {
		hk := healthKey{k.method, "withdraw"}
		cur[hk] = append(cur[hk], k.c)
	}
	out := make([]MethodHealth, 0, len(cur))
	for k, cs := range cur {
		slices.Sort(cs)
		m := MethodHealth{Name: k.name, Kind: k.kind, Currencies: cs}
		if h := s.health[k]; h != nil {
			m.Running, m.Restarts, m.LastOK, m.LastError, m.LastErrorAt = h.running, h.restarts, h.lastOK, h.lastErr, h.lastErrorAt
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b MethodHealth) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Kind, b.Kind)) })
	return out
}
