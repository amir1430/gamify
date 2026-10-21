package auth

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/amir1430/gamify/auth/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User is a player as the admin sees them.
type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

var (
	// ErrIdentityTaken: the identity is already linked to another user.
	ErrIdentityTaken = errors.New("auth: identity belongs to another user")
	// ErrProviderLinked: the user already has another identity at that provider.
	ErrProviderLinked = errors.New("auth: user already has an identity at this provider")
	// ErrLastIdentity: removing it would leave the user no way to log in.
	ErrLastIdentity = errors.New("auth: the user's only login can't be unlinked")
	// ErrNoIdentity: the user has no identity at that provider.
	ErrNoIdentity = errors.New("auth: no such identity")
)

// Users keeps the users table and their identities.
type Users struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

func NewUsers(pool *pgxpool.Pool) *Users { return &Users{pool: pool, q: store.New(pool)} }

// Login is the user behind an identity after a login: Created when this login made the account.
type Login struct {
	ID      int64
	Created bool
}

var errLostRace = errors.New("auth: identity created concurrently")

// Upsert records a login: the identity's user with refreshed names, or a new user and identity on
// the first one. Two first logins at once both get the same user.
func (u *Users) Upsert(ctx context.Context, id Identity) (Login, error) {
	if id.Provider == "" || id.Subject == "" {
		return Login{}, errors.New("auth: identity without provider or subject")
	}
	l, err := u.upsert(ctx, id)
	if errors.Is(err, errLostRace) { // the other login committed: now it's found
		l, err = u.upsert(ctx, id)
	}
	return l, err
}

func (u *Users) upsert(ctx context.Context, id Identity) (Login, error) {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return Login{}, err
	}
	defer tx.Rollback(ctx)
	q := u.q.WithTx(tx)
	userID, err := q.FindIdentity(ctx, store.FindIdentityParams{Provider: id.Provider, Subject: id.Subject})
	switch {
	case err == nil:
		err = q.TouchUser(ctx, store.TouchUserParams{ID: userID, Username: text(id.Username),
			DisplayName: text(id.DisplayName), LanguageCode: text(id.Lang)})
		if err == nil {
			err = tx.Commit(ctx)
		}
		return Login{ID: userID}, err
	case !errors.Is(err, pgx.ErrNoRows):
		return Login{}, err
	}
	userID, err = q.CreateUser(ctx, store.CreateUserParams{Username: text(id.Username),
		DisplayName: text(id.DisplayName), LanguageCode: text(id.Lang)})
	if err != nil {
		return Login{}, err
	}
	// The primary key waits for a concurrent first login's transaction; if it committed, nothing is
	// inserted and this user is rolled back.
	n, err := q.AddIdentity(ctx, store.AddIdentityParams{Provider: id.Provider, Subject: id.Subject, UserID: userID})
	if err != nil {
		return Login{}, err
	}
	if n == 0 {
		return Login{}, errLostRace
	}
	return Login{ID: userID, Created: true}, tx.Commit(ctx)
}

// Link attaches another identity to userID, the connection's user (never one from a payload).
// Linking an identity the user already has is a no-op; one linked to someone else is
// ErrIdentityTaken; a second one at a provider the user has is ErrProviderLinked.
func (u *Users) Link(ctx context.Context, userID int64, id Identity) error {
	if id.Provider == "" || id.Subject == "" {
		return errors.New("auth: identity without provider or subject")
	}
	n, err := u.q.AddIdentity(ctx, store.AddIdentityParams{Provider: id.Provider, Subject: id.Subject, UserID: userID})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "user_identities_one_per_provider" {
		return ErrProviderLinked
	}
	if err != nil || n == 1 {
		return err
	}
	owner, err := u.q.FindIdentity(ctx, store.FindIdentityParams{Provider: id.Provider, Subject: id.Subject})
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrIdentityTaken
	}
	return nil
}

// Unlink removes the user's identity at provider: for support and admins (a login linked by
// someone who had the player's session), never a player RPC, since whoever holds a session could
// then remove the owner's own login. The user's last identity stays (ErrLastIdentity); a session
// already open with the removed login lasts until its realtime token expires.
func (u *Users) Unlink(ctx context.Context, userID int64, provider string) error {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := u.UnlinkTx(ctx, tx, userID, provider); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UnlinkTx is Unlink inside the caller's transaction (an admin writes its audit row in the same
// one). It neither commits nor rolls back; the checks and errors are Unlink's.
func (u *Users) UnlinkTx(ctx context.Context, tx pgx.Tx, userID int64, provider string) error {
	q := u.q.WithTx(tx)
	if _, err := q.LockUser(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNoIdentity
	} else if err != nil {
		return err
	}
	if _, err := q.GetSubject(ctx, store.GetSubjectParams{UserID: userID, Provider: provider}); errors.Is(err, pgx.ErrNoRows) {
		return ErrNoIdentity
	} else if err != nil {
		return err
	}
	if n, err := q.CountIdentities(ctx, userID); err != nil {
		return err
	} else if n <= 1 {
		return ErrLastIdentity
	}
	_, err := q.DeleteIdentity(ctx, store.DeleteIdentityParams{UserID: userID, Provider: provider})
	return err
}

// LinkedIdentity is one login of a user.
type LinkedIdentity struct {
	Provider string    `json:"provider"`
	Subject  string    `json:"subject"`
	LinkedAt time.Time `json:"linked_at"`
}

// Identities lists the user's logins by provider; an unknown user has none.
func (u *Users) Identities(ctx context.Context, userID int64) ([]LinkedIdentity, error) {
	rows, err := u.q.ListIdentities(ctx, userID)
	out := make([]LinkedIdentity, len(rows))
	for i, r := range rows {
		out[i] = LinkedIdentity{Provider: r.Provider, Subject: r.Subject, LinkedAt: r.CreatedAt.Time}
	}
	return out, err
}

// Subject is the user's ID at provider (their Telegram ID, for the Bot API), or ErrNoIdentity.
func (u *Users) Subject(ctx context.Context, userID int64, provider string) (string, error) {
	s, err := u.q.GetSubject(ctx, store.GetSubjectParams{UserID: userID, Provider: provider})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoIdentity
	}
	return s, err
}

// Search lists players newest first, limit rows below beforeID (0 = from the newest). A query
// matches a user ID or any identity's subject (a Telegram ID) exactly, or part of a username
// ("@name" too) or display name.
func (u *Users) Search(ctx context.Context, query string, beforeID int64, limit int32) ([]User, error) {
	p := store.SearchUsersParams{MaxRows: limit}
	if beforeID > 0 {
		p.BeforeID = &beforeID
	}
	if query = strings.TrimPrefix(strings.TrimSpace(query), "@"); query != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(query) + "%"
		p.Pattern, p.Exact = &pattern, &query
		if id, err := strconv.ParseInt(query, 10, 64); err == nil {
			p.ExactID = &id
		}
	}
	rows, err := u.q.SearchUsers(ctx, p)
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = user(r.ID, r.Username, r.DisplayName, r.CreatedAt, r.LastLoginAt)
	}
	return out, err
}

// Get reads players by ID, newest first; unknown IDs are left out.
func (u *Users) Get(ctx context.Context, ids ...int64) ([]User, error) {
	rows, err := u.q.GetUsers(ctx, ids)
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = user(r.ID, r.Username, r.DisplayName, r.CreatedAt, r.LastLoginAt)
	}
	return out, err
}

func user(id int64, username, displayName *string, created, lastLogin pgtype.Timestamptz) User {
	u := User{ID: id, CreatedAt: created.Time}
	if username != nil {
		u.Username = *username
	}
	if displayName != nil {
		u.DisplayName = *displayName
	}
	if lastLogin.Valid {
		u.LastLoginAt = &lastLogin.Time
	}
	return u
}

// text is NULL for "": the provider didn't send it.
func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
