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
