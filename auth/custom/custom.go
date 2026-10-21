// Package custom is the auth provider for a brand with its own accounts: the brand's backend signs
// a short-lived HS256 JWT for its user and the client posts it to /auth/custom as {"token": …}.
//
// Header: exactly {"alg":"HS256"}, optionally with "typ":"JWT". Claims: sub (required, the brand's
// user ID: printable UTF-8, at most 255 bytes), aud (required, must contain Config.Audience, so a
// token the brand made for another service can't log in here), exp (required, at most
// Config.MaxLifetime ahead), nbf (optional), name, preferred_username and locale (optional profile).
// Other claims (iat, iss, jti) are ignored.
package custom

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/amir1430/gamify/auth"
)

// MinSecret is the shortest HS256 key accepted: the hash's own size (RFC 7518 3.2).
const MinSecret = 32

// Leeway absorbs clock skew between the brand's servers and ours.
const Leeway = 30 * time.Second

type Config struct {
	Secret      []byte        // shared with the brand's backend, at least MinSecret bytes
	Audience    string        // required in every token's aud
	MaxLifetime time.Duration // how far ahead exp may be (default 15m): a token is a short-lived bearer credential
}

type Provider struct{ cfg Config }

var _ auth.Provider = (*Provider)(nil)

var b64 = base64.RawURLEncoding.Strict() // one encoding per token: no malleable signatures

func New(cfg Config) (*Provider, error) {
	if len(cfg.Secret) < MinSecret {
		return nil, fmt.Errorf("custom: secret is %d bytes, want at least %d", len(cfg.Secret), MinSecret)
	}
	if cfg.Audience == "" {
		return nil, errors.New("custom: an audience is required")
	}
	if cfg.MaxLifetime == 0 {
		cfg.MaxLifetime = 15 * time.Minute
	}
	cfg.Secret = bytes.Clone(cfg.Secret)
	return &Provider{cfg: cfg}, nil
}

func (*Provider) Name() string { return "custom" }

func (p *Provider) Verify(_ context.Context, credential json.RawMessage, now time.Time) (auth.Identity, error) {
	var c struct {
		Token string `json:"token"`
	}
	if err := strictJSON(credential, &c); err != nil || c.Token == "" {
		return auth.Identity{}, invalid("want {\"token\": …}")
	}
	parts := strings.Split(c.Token, ".")
	if len(parts) != 3 || strings.ContainsAny(c.Token, "\r\n") {
		return auth.Identity{}, invalid("not a JWT")
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return auth.Identity{}, invalid("bad signature encoding")
	}
	// The signature first: nothing in the token is trusted, the header included, until it matches.
	m := hmac.New(sha256.New, p.cfg.Secret)
	m.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(m.Sum(nil), sig) {
		return auth.Identity{}, invalid("bad signature")
	}
	header, err := fields(parts[0])
	if err != nil || len(header) > 2 || str(header["alg"]) != "HS256" || (header["typ"] != nil && str(header["typ"]) != "JWT") ||
		(len(header) == 2 && header["typ"] == nil) {
		return auth.Identity{}, invalid(`header must be {"alg":"HS256"} (typ "JWT" allowed)`)
	}
	claims, err := fields(parts[1])
	if err != nil {
		return auth.Identity{}, invalid("bad claims")
	}
	sub := str(claims["sub"])
	exp, expOK := date(claims["exp"])
	nbf, nbfOK := date(claims["nbf"])
	switch {
	case !utf8.Valid(claims["sub"]) || !printable(sub): // decoding would turn bad bytes into U+FFFD
		return auth.Identity{}, invalid("sub is required: printable UTF-8, at most 255 bytes")
	case !audience(claims["aud"], p.cfg.Audience):
		return auth.Identity{}, invalid("aud must contain " + strconv.Quote(p.cfg.Audience))
	case !expOK:
		return auth.Identity{}, invalid("exp is required")
	case !now.Before(exp):
		return auth.Identity{}, invalid("expired")
	case exp.After(now.Add(p.cfg.MaxLifetime + Leeway)):
		return auth.Identity{}, invalid("exp is too far ahead")
	case claims["nbf"] != nil && (!nbfOK || now.Add(Leeway).Before(nbf)):
		return auth.Identity{}, invalid("not valid yet")
	}
	return auth.Identity{Provider: p.Name(), Subject: sub, Username: str(claims["preferred_username"]),
		DisplayName: str(claims["name"]), Lang: str(claims["locale"])}, nil
}

func invalid(why string) error { return fmt.Errorf("%w: %s", auth.ErrInvalidCredential, why) }

// fields decodes a token part into its members by exact name (encoding/json would match "ALG" to
// alg). Duplicate names are refused.
func fields(part string) (map[string]json.RawMessage, error) {
	b, err := b64.DecodeString(part)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		name := t.(string)
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("duplicate %q", name)
		}
		out[name] = v
	}
	if _, err := dec.Token(); err != nil || dec.More() {
		return nil, errors.New("trailing data")
	}
	return out, nil
}

// str is a JSON string member, or "" when it's missing or not a string.
func str(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

// date reads a NumericDate within a sane range (years 1970..5138).
func date(v json.RawMessage) (time.Time, bool) {
	f, err := strconv.ParseFloat(string(v), 64)
	if v == nil || err != nil || f <= 0 || f >= 1e11 {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

func audience(v json.RawMessage, want string) bool {
	if str(v) == want {
		return true
	}
	var list []string
	if json.Unmarshal(v, &list) != nil {
		return false
	}
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

// printable: a subject must be a stable, loggable key (no NUL for Postgres, no U+FFFD collisions).
func printable(s string) bool {
	if s == "" || len(s) > 255 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func strictJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}
