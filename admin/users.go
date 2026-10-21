package admin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
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

// CreateUser adds a console user with a random 20-char password and returns it (migrates first).
// The email is stored trimmed and lowercased; an email taken in any case is an error. The user
// enrols TOTP on first login. It writes an admin.create audit row in the same transaction.
func CreateUser(ctx context.Context, pool *pgxpool.Pool, email string, role Role) (password string, err error) {
	if !role.Valid() {
		return "", fmt.Errorf("admin: unknown role %q", role)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return "", fmt.Errorf("admin: invalid email %q", email)
	}
	if err := db.Migrate(ctx, pool, Migrations, gooseTable); err != nil {
		return "", err
	}
	password, err = randomPassword()
	if err != nil {
		return "", err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	// The audit row has no acting admin: this runs from the command line, before any exists.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		id, err := q.CreateUser(ctx, store.CreateUserParams{Email: email, Role: string(role), PasswordHash: hash})
		if err != nil {
			return err
		}
		return insertAudit(ctx, q, nil, nil, "admin.create", adminTarget(id), nil,
			map[string]string{"email": email, "role": string(role)}, "")
	})
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", fmt.Errorf("admin: %s already exists", email)
	}
	if err != nil {
		return "", err
	}
	return password, nil
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
