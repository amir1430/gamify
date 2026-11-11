// Package paymentstest has fake payments drivers for brands' tests.
package paymentstest

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/amir1430/gamify/payments"
)

// Receiver records Deposit calls and answers address "addr-<user>". Call Pay to simulate an
// incoming payment once Run has started.
type Receiver struct {
	Method string

	mu    sync.Mutex
	Calls []payments.DepositRequest
	h     *payments.Host
}

func (r *Receiver) Name() string { return r.Method }

func (r *Receiver) Deposit(_ context.Context, req payments.DepositRequest) (payments.DepositInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Calls = append(r.Calls, req)
	return payments.DepositInfo{Address: fmt.Sprintf("addr-%d", req.UserID), Amount: req.Amount}, nil
}

func (r *Receiver) Run(ctx context.Context, h *payments.Host) error {
	r.mu.Lock()
	r.h = h
	r.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

// Pay reports in through the Host Run was given.
func (r *Receiver) Pay(ctx context.Context, in payments.Incoming) error {
	r.mu.Lock()
	h := r.h
	r.mu.Unlock()
	if h == nil {
		return errors.New("paymentstest: receiver not running")
	}
	return h.Received(ctx, in)
}

// Sender is scripted: each new payout takes the next of Next (default paid), or fails with Err.
// Sent lists the distinct payouts; sending an ID again returns its first answer.
type Sender struct {
	Method   string
	Next     []Result
	Err      error
	Statuses map[int64]Result // what Status answers; unset: the recorded result, else unknown

	mu      sync.Mutex
	Sent    []payments.Payout
	results map[int64]Result
}

func (s *Sender) Name() string { return s.Method }

func (s *Sender) ValidateDestination(dest string) error {
	if dest == "" || dest == "bad" {
		return errors.New("bad destination")
	}
	return nil
}

func (s *Sender) Send(_ context.Context, p payments.Payout) (payments.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.results[p.ID]; ok {
		return r, nil
	}
	if s.Err != nil {
		return payments.Result{}, s.Err
	}
	r := payments.Result{State: payments.StatePaid, Ref: fmt.Sprintf("ref-%d", p.ID)}
	if len(s.Next) > 0 {
		r, s.Next = s.Next[0], s.Next[1:]
	}
	if s.results == nil {
		s.results = map[int64]Result{}
	}
	s.results[p.ID] = r
	s.Sent = append(s.Sent, p)
	return r, nil
}

// Result is payments.Result, so tests can write paymentstest.Result.
type Result = payments.Result

func (s *Sender) Status(_ context.Context, id int64) (payments.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.Statuses[id]; ok {
		return r, nil
	}
	if r, ok := s.results[id]; ok {
		return r, nil
	}
	return payments.Result{State: payments.StateUnknown}, nil
}
