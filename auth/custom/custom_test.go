package custom

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amir1430/gamify/auth"
)

var (
	secret = []byte("0123456789abcdef0123456789abcdef")
	now    = time.Unix(1_700_000_000, 0)
)

func sign(t *testing.T, key []byte, header, claims any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	in := enc(header) + "." + enc(claims)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(in))
	return in + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// signRaw signs header and claims byte for byte (json.Marshal would repair invalid UTF-8).
func signRaw(header, claims string) string {
	in := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString([]byte(claims))
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(in))
	return in + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func cred(token string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"token": token})
	return b
}

var hs256 = map[string]string{"alg": "HS256", "typ": "JWT"}

func TestVerify(t *testing.T) {
	p, err := New(Config{Secret: secret, Audience: "game"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "custom" {
		t.Fatalf("name = %q", p.Name())
	}
	token := sign(t, secret, hs256, map[string]any{"sub": "u-42", "aud": "game", "exp": now.Add(5 * time.Minute).Unix(),
		"name": "Bob", "preferred_username": "bob", "locale": "en"})
	id, err := p.Verify(context.Background(), cred(token), now)
	if err != nil {
		t.Fatal(err)
	}
	want := auth.Identity{Provider: "custom", Subject: "u-42", Username: "bob", DisplayName: "Bob", Lang: "en"}
	if !reflect.DeepEqual(id, want) {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
}

func TestVerifyRejects(t *testing.T) {
	p, _ := New(Config{Secret: secret, Audience: "game"})
	exp := now.Add(5 * time.Minute).Unix()
	ok := map[string]any{"sub": "u-42", "aud": "game", "exp": exp}
	with := func(k string, v any) map[string]any {
		m := map[string]any{"sub": "u-42", "aud": "game", "exp": exp}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	good := sign(t, secret, hs256, ok)
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"admin","exp":9999999999}`)) + "." + parts[2]
	for name, c := range map[string]json.RawMessage{
		"wrong secret":   cred(sign(t, []byte("another-secret-another-secret-xx"), hs256, ok)),
		"alg none":       cred(sign(t, secret, map[string]string{"alg": "none"}, ok)),
		"alg HS512":      cred(sign(t, secret, map[string]string{"alg": "HS512"}, ok)),
		"expired":        cred(sign(t, secret, hs256, with("exp", now.Unix()))),
		"no exp":         cred(sign(t, secret, hs256, with("exp", nil))),
		"exp too far":    cred(sign(t, secret, hs256, with("exp", now.Add(time.Hour).Unix()))),
		"exp huge":       cred(sign(t, secret, hs256, with("exp", 1e300))),
		"no sub":         cred(sign(t, secret, hs256, with("sub", nil))),
		"long sub":       cred(sign(t, secret, hs256, with("sub", strings.Repeat("x", 256)))),
		"nul in sub":     cred(sign(t, secret, hs256, with("sub", "a\x00b"))),
		"bad utf8 sub":   cred(signRaw(`{"alg":"HS256"}`, "{\"sub\":\"\xff\",\"aud\":\"game\",\"exp\":"+fmt.Sprint(exp)+"}")),
		"no aud":         cred(sign(t, secret, hs256, with("aud", nil))),
		"other aud":      cred(sign(t, secret, hs256, with("aud", "admin"))),
		"other aud list": cred(sign(t, secret, hs256, with("aud", []string{"admin", "web"}))),
		"not yet valid":  cred(sign(t, secret, hs256, with("nbf", now.Add(time.Minute).Unix()))),
		"ALG upper":      cred(sign(t, secret, map[string]string{"ALG": "HS256"}, ok)),
		"crit":           cred(sign(t, secret, map[string]any{"alg": "HS256", "crit": []string{"x"}, "x": 1}, ok)),
		"typ other":      cred(sign(t, secret, map[string]string{"alg": "HS256", "typ": "at+jwt"}, ok)),
		"sig newline":    cred(parts[0] + "." + parts[1] + "." + parts[2][:10] + "\n" + parts[2][10:]),
		"tampered":       cred(tampered),
		"two parts":      cred(parts[0] + "." + parts[1]),
		"empty":          cred(""),
		"not json":       json.RawMessage(`token`),
		"unknown field":  json.RawMessage(`{"token":"` + good + `","user_id":1}`),
		"signature junk": cred(parts[0] + "." + parts[1] + ".!!!"),
	} {
		if _, err := p.Verify(context.Background(), c, now); !errors.Is(err, auth.ErrInvalidCredential) {
			t.Errorf("%s: err = %v, want ErrInvalidCredential", name, err)
		}
	}
}

func TestAudienceList(t *testing.T) {
	p, _ := New(Config{Secret: secret, Audience: "game"})
	token := sign(t, secret, hs256, map[string]any{"sub": "u-42", "aud": []string{"web", "game"}, "exp": now.Add(time.Minute).Unix()})
	if _, err := p.Verify(context.Background(), cred(token), now); err != nil {
		t.Fatal(err)
	}
}

func TestNewRefusesShortSecret(t *testing.T) {
	if _, err := New(Config{Secret: []byte("short"), Audience: "game"}); err == nil {
		t.Fatal("a 5-byte HS256 secret was accepted")
	}
	if _, err := New(Config{Secret: secret}); err == nil {
		t.Fatal("no audience accepted")
	}
}
