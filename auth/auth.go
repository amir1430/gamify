// Package auth is login: a Provider verifies a credential (Telegram initData, a brand's signed
// token) into an Identity, and Users maps identities to the platform's users. A user is an internal
// ID with one or more linked identities. Providers are drivers in auth/<name>.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Identity is who a provider says the caller is. Provider and Subject identify them; the rest is
// profile data refreshed on every login.
type Identity struct {
	Provider    string            // "telegram", "custom"
	Subject     string            // the provider's ID for the person: a Telegram ID, a brand's user ID
	Username    string            // may be empty
	DisplayName string            // may be empty
	Lang        string            // IETF tag, may be empty
	Meta        map[string]string // provider extras, e.g. telegram start_param
}

// Provider verifies one kind of credential. Name is its route (POST /auth/{name}) and the
// Identity.Provider it returns.
type Provider interface {
	Name() string
	// Verify returns ErrInvalidCredential (wrapped or not) for anything it doesn't accept.
	Verify(ctx context.Context, credential json.RawMessage, now time.Time) (Identity, error)
}

var ErrInvalidCredential = errors.New("auth: invalid credential")
