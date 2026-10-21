package admin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"strings"

	"github.com/amir1430/gamify/admin/internal/store"
	"github.com/amir1430/gamify/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// passwordAlphabet has no 0/O/1/l/I, so a password read off a terminal can be typed back.
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

const passwordLen = 20

// ErrEmailTaken is what CreateUser returns (wrapped) for an email already in use, in any case.
var ErrEmailTaken = errors.New("admin: email already in use")

// CreateUser adds a console user with a random 20-char password and returns it (migrates first).
// The email is stored trimmed and lowercased; an email taken in any case is ErrEmailTaken. The user
// enrols TOTP on first login. It writes an admin.create audit row in the same transaction.
func CreateUser(ctx context.Context, pool *pgxpool.Pool, email string, role Role) (password string, err error) {
	email, err = checkNewUser(email, role)
	if err != nil {
		return "", err
	}
	if err := db.Migrate(ctx, pool, Migrations, gooseTable); err != nil {
		return "", err
	}
	password, hash, err := newCredentials()
	if err != nil {
		return "", err
	}
	// The audit row has no acting admin: this runs from the command line, before any exists.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return insertUser(ctx, tx, nil, nil, email, role, hash)
	})
	if err != nil {
		return "", err
	}
	return password, nil
}

// checkNewUser validates a new user's role and email and returns the email trimmed and lowercased.
func checkNewUser(email string, role Role) (string, error) {
	if !role.Valid() {
		return "", fmt.Errorf("admin: unknown role %q", role)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return "", fmt.Errorf("admin: invalid email %q", email)
	}
	return email, nil
}

// newCredentials is a random password and its hash (slow: call it outside a transaction).
func newCredentials() (password, hash string, err error) {
	if password, err = randomPassword(); err != nil {
		return "", "", err
	}
	if hash, err = hashPassword(password); err != nil {
		return "", "", err
	}
	return password, hash, nil
}

// insertUser adds the user and its admin.create audit row inside tx. actor is the console user
// creating it (nil: the command line); a taken email is ErrEmailTaken, which aborts tx.
func insertUser(ctx context.Context, tx pgx.Tx, actor *int64, ip *netip.Addr, email string, role Role, hash string) error {
	q := store.New(tx)
	id, err := q.CreateUser(ctx, store.CreateUserParams{Email: email, Role: string(role), PasswordHash: hash})
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: %s", ErrEmailTaken, email)
	}
	if err != nil {
		return err
	}
	return insertAudit(ctx, q, actor, ip, "admin.create", adminTarget(id), nil,
		adminView{Email: email, Role: role}, "")
}

func randomPassword() (string, error) {
	b := make([]byte, passwordLen)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[n.Int64()]
	}
	return string(b), nil
}
