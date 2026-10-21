package fairness

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/amir1430/gamify/fairness/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	sealer *Sealer
	guards []Guard
}

func NewService(pool *pgxpool.Pool, sealer *Sealer) *Service {
	return &Service{pool: pool, q: store.New(pool), sealer: sealer}
}

// Ensure gives the user an active and a next seed pair if missing. Idempotent and race-safe
// (partial unique indexes + ON CONFLICT DO NOTHING).
func (s *Service) Ensure(ctx context.Context, userID int64) error {
	clientSeed := NewClientSeed()
	seed, hash := NewServerSeed()
	if err := s.q.CreateActiveSeedPair(ctx, store.CreateActiveSeedPairParams{
		UserID: userID, ServerSeedEnc: s.sealer.Seal(userID, seed), KeyID: s.sealer.KeyID,
		ServerSeedHash: hash, ClientSeed: clientSeed,
	}); err != nil {
		return err
	}
	return s.createNext(ctx, s.q, userID, clientSeed)
}

func (s *Service) createNext(ctx context.Context, q *store.Queries, userID int64, clientSeed string) error {
	seed, hash := NewServerSeed()
	return q.CreateNextSeedPair(ctx, store.CreateNextSeedPairParams{
		UserID: userID, ServerSeedEnc: s.sealer.Seal(userID, seed), KeyID: s.sealer.KeyID,
		ServerSeedHash: hash, ClientSeed: clientSeed,
	})
}

// Draw is the randomness for one bet: the per-bet Hash (Dice, Limbo, Mines) and, if asked for,
// cursor Floats (Keno). Server-internal: never send it to the client while the seed is active.
type Draw struct {
	SeedPairID int64
	Nonce      int64
	Hash       string
	Hash512    string // Plinko only
	Floats     []float64
}

// Next consumes the next nonce of the user's active seed pair and returns its Draw with n floats.
// Must run inside the transaction that inserts the bet, so a rolled-back bet doesn't burn the nonce.
// Creates the user's seed pairs if they have none (e.g. after old pairs were retired).
func (s *Service) Next(ctx context.Context, tx pgx.Tx, userID int64, n int) (Draw, error) {
	row, err := s.q.WithTx(tx).NextNonce(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := s.Ensure(ctx, userID); err != nil {
			return Draw{}, err
		}
		row, err = s.q.WithTx(tx).NextNonce(ctx, userID)
	}
	if err != nil {
		return Draw{}, fmt.Errorf("fairness: next nonce: %w", err)
	}
	seed, err := s.sealer.Open(userID, row.KeyID, row.ServerSeedEnc)
	if err != nil {
		return Draw{}, err
	}
	key := Key(seed)
	d := Draw{SeedPairID: row.ID, Nonce: row.Nonce, Hash: Hash(key, row.ClientSeed, row.Nonce),
		Hash512: Hash512(key, row.ClientSeed, row.Nonce)}
	if n > 0 {
		d.Floats = Floats(key, row.ClientSeed, uint64(row.Nonce), n)
	}
	return d, nil
}

// DB is a pool or a transaction. HashAt and RoundHashesAt read through the caller's transaction
// when there is one: taking a second pool connection while holding one deadlocks under load
// (every connection held by a transaction waiting for another).
type DB = store.DBTX

func (s *Service) keyAt(ctx context.Context, db DB, userID, seedPairID int64) (key []byte, clientSeed string, err error) {
	row, err := store.New(db).GetSeedPairSecret(ctx, store.GetSeedPairSecretParams{ID: seedPairID, UserID: userID})
	if err != nil {
		return nil, "", fmt.Errorf("fairness: seed pair %d: %w", seedPairID, err)
	}
	seed, err := s.sealer.Open(userID, row.KeyID, row.ServerSeedEnc)
	if err != nil {
		return nil, "", err
	}
	return Key(seed), row.ClientSeed, nil
}

// HashAt recomputes an earlier bet's Hash (e.g. a Mines layout on each reveal), so game secrets
// never need to be stored. Server-internal only: never return it while the seed is active.
func (s *Service) HashAt(ctx context.Context, db DB, userID, seedPairID, nonce int64) (string, error) {
	key, client, err := s.keyAt(ctx, db, userID, seedPairID)
	if err != nil {
		return "", err
	}
	return Hash(key, client, nonce), nil
}

// RoundHashesAt returns the RoundHash of rounds 0..n-1 of a multi-round bet (Hi-Lo cards).
// Server-internal only, like HashAt.
func (s *Service) RoundHashesAt(ctx context.Context, db DB, userID, seedPairID, nonce int64, n int) ([]string, error) {
	key, client, err := s.keyAt(ctx, db, userID, seedPairID)
	if err != nil {
		return nil, err
	}
	out := make([]string, n)
	for r := range n {
		out[r] = RoundHash(key, client, nonce, r)
	}
	return out, nil
}

// Info is what the player may see before playing: hashes only, never seeds.
type Info struct {
	ServerSeedHash     string `json:"server_seed_hash"`
	ClientSeed         string `json:"client_seed"`
	Nonce              int64  `json:"nonce"`
	NextServerSeedHash string `json:"next_server_seed_hash"`
}

// Info commits the user's seeds if they have none yet, so the hash is shown before the first bet.
func (s *Service) Info(ctx context.Context, userID int64) (Info, error) {
	if err := s.Ensure(ctx, userID); err != nil {
		return Info{}, err
	}
	rows, err := s.q.GetSeedInfo(ctx, userID)
	if err != nil {
		return Info{}, err
	}
	var info Info
	for _, r := range rows {
		if r.Status == "active" {
			info.ServerSeedHash, info.ClientSeed, info.Nonce = hex.EncodeToString(r.ServerSeedHash), r.ClientSeed, r.Nonce
		} else {
			info.NextServerSeedHash = hex.EncodeToString(r.ServerSeedHash)
		}
	}
	return info, nil
}

// Revealed is a retired seed pair the player can now verify.
type Revealed struct {
	ServerSeed     string `json:"server_seed"`
	ServerSeedHash string `json:"server_seed_hash"`
	ClientSeed     string `json:"client_seed"`
	Nonces         int64  `json:"nonces"` // bets played: nonces 0..Nonces-1
}

var ErrNoSeedPair = errors.New("fairness: no seed pair")

// Guard can veto a rotation. It runs inside the rotation's transaction, after the active seed row is
// locked, so no new game can draw from the seed between the check and the reveal.
type Guard func(ctx context.Context, tx pgx.Tx, userID int64) error

// Guard registers a check every rotation must pass (a multi-step game: no live game, since revealing
// the seed reveals its outcome). Call it before the app runs.
func (s *Service) Guard(g Guard) { s.guards = append(s.guards, g) }

// Rotate reveals the active seed, promotes the next one with clientSeed (empty keeps the current one),
// and pre-commits a new next seed. Every registered Guard must pass.
func (s *Service) Rotate(ctx context.Context, userID int64, clientSeed string) (Revealed, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Revealed{}, err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	active, err := q.LockActiveSeedPair(ctx, userID) // same row lock as NextNonce: no bet can interleave
	if errors.Is(err, pgx.ErrNoRows) {
		return Revealed{}, ErrNoSeedPair
	}
	if err != nil {
		return Revealed{}, err
	}
	for _, g := range s.guards {
		if err := g(ctx, tx, userID); err != nil {
			return Revealed{}, err
		}
	}
	seed, err := s.sealer.Open(userID, active.KeyID, active.ServerSeedEnc)
	if err != nil {
		return Revealed{}, err
	}
	if clientSeed == "" {
		clientSeed = active.ClientSeed
	}
	if err := q.RevealSeedPair(ctx, active.ID); err != nil {
		return Revealed{}, err
	}
	if _, err := q.PromoteNextSeedPair(ctx, store.PromoteNextSeedPairParams{UserID: userID, ClientSeed: clientSeed}); err != nil {
		return Revealed{}, err
	}
	if err := s.createNext(ctx, q, userID, NewClientSeed()); err != nil {
		return Revealed{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Revealed{}, err
	}
	return Revealed{
		ServerSeed:     string(Key(seed)), // the hex text: what the verifier takes
		ServerSeedHash: hex.EncodeToString(active.ServerSeedHash),
		ClientSeed:     active.ClientSeed,
		Nonces:         active.Nonce,
	}, nil
}

// A pot (PvP table) has its own server seed, shared by its players:
// sealed with AAD potAAD, public only once the pot is settled.
const potAAD = 0

// PotSeed is a sealed pot seed and its public commitment sha256(seed text).
type PotSeed struct {
	Enc               []byte
	KeyID, Commitment string
}

func (s *Service) NewPotSeed() PotSeed {
	seed, h := NewServerSeed()
	return PotSeed{Enc: s.sealer.Seal(potAAD, seed), KeyID: s.sealer.KeyID, Commitment: hex.EncodeToString(h)}
}

// OpenPotSeed returns the seed's 64-char hex text (the HMAC key).
func (s *Service) OpenPotSeed(enc []byte, keyID string) (string, error) {
	seed, err := s.sealer.Open(potAAD, keyID, enc)
	if err != nil {
		return "", err
	}
	return string(Key(seed)), nil
}

// Pair is a seed pair as the admin lists it. ServerSeed is set only once the pair is revealed.
type Pair struct {
	ID             int64      `json:"id"`
	Status         string     `json:"status"` // active, next, revealed
	ServerSeedHash string     `json:"server_seed_hash"`
	ClientSeed     string     `json:"client_seed"`
	Nonce          int64      `json:"nonce"`
	ServerSeed     string     `json:"server_seed,omitempty"` // revealed pairs only
	CreatedAt      time.Time  `json:"created_at"`
	RevealedAt     *time.Time `json:"revealed_at"`
}

// PairsOf lists the user's seed pairs, newest first; beforeID 0 starts at the newest. Unrevealed
// seeds never leave the database: the query returns the sealed seed of revealed pairs only.
func (s *Service) PairsOf(ctx context.Context, userID, beforeID int64, limit int32) ([]Pair, error) {
	arg := store.ListSeedPairsParams{UserID: userID, MaxRows: limit}
	if beforeID > 0 {
		arg.BeforeID = &beforeID
	}
	rows, err := s.q.ListSeedPairs(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]Pair, 0, len(rows))
	for _, r := range rows {
		p := Pair{ID: r.ID, Status: r.Status, ServerSeedHash: hex.EncodeToString(r.ServerSeedHash),
			ClientSeed: r.ClientSeed, Nonce: r.Nonce, CreatedAt: r.CreatedAt.Time}
		if r.RevealedAt.Valid {
			p.RevealedAt = &r.RevealedAt.Time
		}
		if r.RevealedEnc != nil {
			seed, err := s.sealer.Open(userID, r.KeyID, r.RevealedEnc)
			if err != nil {
				return nil, err
			}
			p.ServerSeed = string(Key(seed))
		}
		out = append(out, p)
	}
	return out, nil
}
