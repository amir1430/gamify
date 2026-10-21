package dice

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/games/dice/internal/store"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Game is Dice's ledger namespace: never change it once bets moved money.
const Game ledger.Game = 1

const (
	HouseEdgeBP = 200 // 2%, as BC.Game
	MaxBet      = 1_000_000
	// Win chance limits, in hundredths of a percent (100 = 1.00%).
	MinWinCount = 100
	MaxWinCount = 9700 // 1.01×; at 98% a win would pay 1.00×
	// MaxPayout: the best win of a max bet (98× at 1%), so the default adds no cap; admins can set
	// a lower one (gameconfig). Bets that could pay more are rejected, never silently capped.
	MaxPayout = MaxBet * (Outcomes - HouseEdgeBP) / MinWinCount
)

var (
	errBadRequest   = gameconfig.ErrInvalid
	errInsufficient = &realtime.ClientError{Code: 1101, Message: "insufficient balance"}
)

type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	fair   *fairness.Service
	ledger *ledger.Ledger
	game   *gameconfig.Game
}

func NewService(pool *pgxpool.Pool, fair *fairness.Service, l *ledger.Ledger, cfg *gameconfig.Service) *Service {
	return &Service{pool: pool, q: store.New(pool), fair: fair, ledger: l, game: cfg.Game("dice", MaxBet, MaxPayout, nil)}
}

type BetRequest struct {
	Amount   int64           `json:"amount"`
	Currency ledger.Currency `json:"currency"` // "" = the default currency
	Target   int             `json:"target"`   // hundredths, 0..10000
	Over     bool            `json:"over"`
	IdemKey  string          `json:"idem_key"`
}

type BetResult struct {
	BetID      int64  `json:"bet_id"`
	SeedPairID int64  `json:"seed_pair_id"`
	Nonce      int64  `json:"nonce"`
	Roll       int    `json:"roll"`
	Target     int    `json:"target"`
	Over       bool   `json:"over"`
	Win        bool   `json:"win"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Payout     int64  `json:"payout"`
}

func (r BetRequest) valid(lim gameconfig.Limits) bool {
	if r.Target < 0 || r.Target > Outcomes ||
		r.IdemKey == "" || len(r.IdemKey) > 64 {
		return false
	}
	wc := WinCount(r.Target, r.Over)
	p := Payout(r.Amount, wc, HouseEdgeBP)
	return wc >= MinWinCount && wc <= MaxWinCount && ledger.Wins(r.Amount, p) && p <= lim.MaxPayout
}

// check applies the game's current limits to a new bet. A retry skips it: it returns its bet even
// if the limits changed since.
func (s *Service) check(req *BetRequest) error {
	cur, lim, err := s.game.Stake(req.Currency, "", req.Amount)
	if err != nil {
		return err
	}
	if req.Currency = cur; !req.valid(lim) {
		return errBadRequest
	}
	return nil
}

// Bet places and settles one dice bet. Retrying with the same IdemKey returns the same bet.
func (s *Service) Bet(ctx context.Context, userID int64, req BetRequest) (BetResult, error) {
	bet, err := s.q.GetDiceBetByKey(ctx, store.GetDiceBetByKeyParams{UserID: userID, IdempotencyKey: req.IdemKey})
	if errors.Is(err, pgx.ErrNoRows) {
		if err = s.check(&req); err == nil {
			bet, err = s.create(ctx, userID, req)
		}
	}
	if err != nil {
		return BetResult{}, err
	}
	return s.settle(ctx, bet)
}

// create draws the outcome and records the bet as pending, in one transaction with the nonce.
func (s *Service) create(ctx context.Context, userID int64, req BetRequest) (store.DiceBet, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.DiceBet{}, err
	}
	defer tx.Rollback(ctx)

	draw, err := s.fair.Next(ctx, tx, userID, 0)
	if err != nil {
		return store.DiceBet{}, err
	}
	roll := Roll(draw.Hash)
	var payout int64
	if Wins(roll, req.Target, req.Over) {
		payout = Payout(req.Amount, WinCount(req.Target, req.Over), HouseEdgeBP)
	}
	bet, err := s.q.WithTx(tx).InsertDiceBet(ctx, store.InsertDiceBetParams{
		UserID: userID, IdempotencyKey: req.IdemKey, SeedPairID: draw.SeedPairID, Nonce: draw.Nonce,
		Amount: req.Amount, Currency: string(req.Currency), Target: int32(req.Target), Over: req.Over, Roll: int32(roll), Payout: payout,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Same key raced in concurrently: drop our nonce (rollback) and use theirs.
		tx.Rollback(ctx)
		return s.q.GetDiceBetByKey(ctx, store.GetDiceBetByKeyParams{UserID: userID, IdempotencyKey: req.IdemKey})
	}
	if err != nil {
		return store.DiceBet{}, err
	}
	return bet, tx.Commit(ctx)
}

// settle moves the money for a recorded bet. Safe to repeat: transfer IDs are derived from the bet ID.
func (s *Service) settle(ctx context.Context, bet store.DiceBet) (BetResult, error) {
	if bet.Status == "pending" {
		err := s.ledger.SettleInstant(ctx, Game, ledger.Currency(bet.Currency), bet.ID, bet.UserID, bet.Amount, bet.Payout)
		switch {
		case errors.Is(err, ledger.ErrInsufficientFunds):
			bet.Status = "voided"
		case err != nil:
			return BetResult{}, err // stays pending; retry or the reconciler finishes it
		default:
			bet.Status = "settled"
		}
		if err := s.q.SetDiceBetStatus(ctx, store.SetDiceBetStatusParams{ID: bet.ID, Status: bet.Status}); err != nil {
			return BetResult{}, err
		}
	}
	if bet.Status == "voided" {
		return BetResult{}, errInsufficient
	}
	return BetResult{
		BetID: bet.ID, SeedPairID: bet.SeedPairID, Nonce: bet.Nonce,
		Roll: int(bet.Roll), Target: int(bet.Target), Over: bet.Over,
		Win: Wins(int(bet.Roll), int(bet.Target), bet.Over), Amount: bet.Amount, Currency: bet.Currency, Payout: bet.Payout,
	}, nil
}

// Reconcile finishes bets left pending by a crash or a ledger outage.
func (s *Service) Reconcile(ctx context.Context) {
	bets, err := s.q.ListStalePendingDiceBets(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "dice reconcile: list", "err", err)
		return
	}
	for _, b := range bets {
		if _, err := s.settle(ctx, b); err != nil && !errors.Is(err, errInsufficient) {
			slog.ErrorContext(ctx, "dice reconcile: settle", "bet", b.ID, "err", err)
		}
	}
}

// BetRow is a dice bet's public columns, for the admin's view (games/dice/diceadmin). It never
// holds seed data. Target and Roll are hundredths; Amount and Payout minor units of Currency.
type BetRow struct {
	ID        int64
	UserID    int64
	Amount    int64
	Currency  string
	Target    int32
	Over      bool
	Roll      int32
	Payout    int64
	Status    string // pending, settled or voided
	CreatedAt time.Time
}

// ListBets lists bets newest first: one user's (userID non-nil) or everyone's, with status ""
// meaning any status. before 0 is the first page, else only bets with a smaller ID.
func (s *Service) ListBets(ctx context.Context, userID *int64, status string, before int64, limit int32) ([]BetRow, error) {
	var st *string
	var bid *int64
	if status != "" {
		st = &status
	}
	if before > 0 {
		bid = &before
	}
	var rows []store.DiceBet
	var err error
	if userID != nil {
		rows, err = s.q.ListDiceBetsByUser(ctx, store.ListDiceBetsByUserParams{UserID: *userID, Status: st, BeforeID: bid, MaxRows: limit})
	} else {
		rows, err = s.q.ListDiceBets(ctx, store.ListDiceBetsParams{Status: st, BeforeID: bid, MaxRows: limit})
	}
	if err != nil {
		return nil, err
	}
	out := make([]BetRow, len(rows))
	for i, r := range rows {
		out[i] = BetRow{ID: r.ID, UserID: r.UserID, Amount: r.Amount, Currency: r.Currency, Target: r.Target,
			Over: r.Over, Roll: r.Roll, Payout: r.Payout, Status: r.Status, CreatedAt: r.CreatedAt.Time}
	}
	return out, nil
}

// History lists bets newest first for the admin (an app.BetSource): userID 0 means every user.
// Detail holds the bet's public fields only, never seed data.
func (s *Service) History(ctx context.Context, userID, beforeID int64, limit int32) ([]app.Bet, error) {
	var user *int64
	if userID > 0 {
		user = &userID
	}
	rows, err := s.ListBets(ctx, user, "", beforeID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]app.Bet, 0, len(rows))
	for _, r := range rows {
		detail, err := json.Marshal(struct {
			Target int32 `json:"target"`
			Over   bool  `json:"over"`
			Roll   int32 `json:"roll"`
		}{r.Target, r.Over, r.Roll})
		if err != nil {
			return nil, err
		}
		out = append(out, app.Bet{Game: "dice", ID: r.ID, UserID: r.UserID, Currency: ledger.Currency(r.Currency),
			Amount: r.Amount, Payout: r.Payout, Status: r.Status, Detail: detail, At: r.CreatedAt})
	}
	return out, nil
}
