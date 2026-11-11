package payments

import (
	"context"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments/internal/store"
)

// FakeHost is the Host Run would get, for tests that drive a Receiver without running the app.
func FakeHost(s *Service, method string) *Host { return &Host{Mux: s.a.HTTP, Method: method, s: s} }

// LeaveCrediting inserts a deposit row as Received does before its ledger call, and stops there.
func LeaveCrediting(ctx context.Context, s *Service, method, ext string, user int64, cur string, amount int64) error {
	_, err := s.q.InsertDeposit(ctx, store.InsertDepositParams{Method: method, Currency: cur, ExternalID: ext,
		UserID: user, Amount: amount, Status: "crediting"})
	return err
}

// LeaveTarget stores an open address target for the user.
func LeaveTarget(ctx context.Context, s *Service, method string, user int64, cur, addr string) error {
	_, err := s.q.InsertTarget(ctx, store.InsertTargetParams{Method: method, Currency: cur, UserID: user, Address: addr})
	return err
}

// LeaveHolding inserts a withdrawal row as the RPC does before its ledger hold, and stops there.
func LeaveHolding(ctx context.Context, s *Service, user int64, cur, method, key string, amount int64) (int64, error) {
	r, err := s.q.InsertWithdrawal(ctx, store.InsertWithdrawalParams{UserID: user, Currency: cur, Method: method,
		Amount: amount, Destination: "addr", IdemKey: key})
	return r.ID, err
}

// Running is how many Receiver Runs Register started.
func Running(s *Service) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

// DepositRPC is the payments.deposit RPC, for tests that need its plain errors.
func DepositRPC(ctx context.Context, s *Service, user int64, c, method string, amount int64) (DepositInfo, error) {
	return s.depositRPC(ctx, user, depositReq{Currency: ledger.Currency(c), Method: method, Amount: amount})
}

// WaitSends waits for the sends the withdraw RPC started in the background.
func WaitSends(s *Service) { s.Wait() }

// Advance is advance, for tests that race it.
func Advance(ctx context.Context, s *Service, id int64) error { return s.advance(ctx, id) }

// RunReceiver runs the named Receiver's Run once, as Register's worker does.
func RunReceiver(ctx context.Context, s *Service, name string) error { return s.runReceiver(ctx, name) }
