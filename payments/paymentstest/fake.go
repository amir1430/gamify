// Package paymentstest has fake payments drivers for brands' tests. They are safe to script and
// read while the payments service calls them from other goroutines.
package paymentstest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/amir1430/gamify/payments"
)

// Receiver records Deposit calls and answers address "addr-<user>"; with Invoices set, a request
// for an amount gets an invoice "inv-<user>-<amount>" too. Call Pay to simulate an
// incoming payment once Run has started.
type Receiver struct {
	Method   string
	Invoices bool // set before use

	mu    sync.Mutex
	calls []payments.DepositRequest
	h     *payments.Host
}

// DepositCalls lists the Deposit requests so far.
func (r *Receiver) DepositCalls() []payments.DepositRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// Ready reports whether Run has started, so Pay works.
func (r *Receiver) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.h != nil
}

func (r *Receiver) Name() string { return r.Method }

func (r *Receiver) Deposit(_ context.Context, req payments.DepositRequest) (payments.DepositInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	info := payments.DepositInfo{Address: fmt.Sprintf("addr-%d", req.UserID), Amount: req.Amount}
	if r.Invoices && req.Amount > 0 {
		info.InvoiceID = fmt.Sprintf("inv-%d-%d", req.UserID, req.Amount)
	}
	return info, nil
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

// Sender is scripted: each new payout takes the next result of SetNext (default paid, Ref
// "ref-<id>"), or fails with SetErr's error. Sending an ID again returns its first answer. Status
// answers SetStatus's result for the ID, else the recorded one, else StateUnknown.
type Sender struct {
	Method string

	mu        sync.Mutex
	next      []payments.Result
	err       error
	statusErr error
	statuses  map[int64]payments.Result
	sent      []payments.Payout
	results   map[int64]payments.Result
}

// SetNext queues the results of the next new payouts.
func (s *Sender) SetNext(rs ...payments.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next = append(s.next, rs...)
}

// SetErr makes Send fail with err (nothing is recorded); nil sends again.
func (s *Sender) SetErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// SetStatusErr makes Status fail with err; nil answers again.
func (s *Sender) SetStatusErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusErr = err
}

// SetStatus sets what Status answers for id.
func (s *Sender) SetStatus(id int64, r payments.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statuses == nil {
		s.statuses = map[int64]payments.Result{}
	}
	s.statuses[id] = r
}

// SentPayouts lists the distinct payouts sent so far.
func (s *Sender) SentPayouts() []payments.Payout {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
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
	if s.err != nil {
		return payments.Result{}, s.err
	}
	r := payments.Result{State: payments.StatePaid, Ref: fmt.Sprintf("ref-%d", p.ID)}
	if len(s.next) > 0 {
		r, s.next = s.next[0], s.next[1:]
	}
	if s.results == nil {
		s.results = map[int64]payments.Result{}
	}
	s.results[p.ID] = r
	s.sent = append(s.sent, p)
	return r, nil
}

func (s *Sender) Status(_ context.Context, id int64) (payments.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusErr != nil {
		return payments.Result{}, s.statusErr
	}
	if r, ok := s.statuses[id]; ok {
		return r, nil
	}
	if r, ok := s.results[id]; ok {
		return r, nil
	}
	return payments.Result{State: payments.StateUnknown}, nil
}
