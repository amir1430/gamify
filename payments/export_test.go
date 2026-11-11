package payments

import (
	"context"

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
