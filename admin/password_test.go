package admin

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestPassword(t *testing.T) {
	h, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q", h)
	}
	if !checkPassword(h, "correct horse") {
		t.Fatal("right password refused")
	}
	if checkPassword(h, "correct horsE") {
		t.Fatal("wrong password accepted")
	}
	h2, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if h == h2 {
		t.Fatal("two hashes of the same password are equal")
	}
	for _, bad := range []string{"", "$argon2id$", "$argon2i$v=19$m=19456,t=2,p=1$AAAA$AAAA", h + "$x", strings.Replace(h, "m=19456", "m=x", 1)} {
		if checkPassword(bad, "correct horse") {
			t.Fatalf("malformed hash %q accepted", bad)
		}
	}
}

func TestPasswordParamCaps(t *testing.T) {
	// A stored hash can't make a login burn unbounded memory or time.
	for _, bad := range []string{
		"$argon2id$v=19$m=2097153,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if checkPassword(bad, "x") {
			t.Fatalf("%q accepted", bad)
		}
	}
	// t = 17 with the right password: refused for its cost alone (cheap to build with m = 8).
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("x"), salt, 17, 8, 1, 32)
	slow := fmt.Sprintf("$argon2id$v=19$m=8,t=17,p=1$%s$%s", b64.EncodeToString(salt), b64.EncodeToString(key))
	if checkPassword(slow, "x") {
		t.Fatal("t=17 accepted")
	}
	if ok := fmt.Sprintf("$argon2id$v=19$m=8,t=16,p=1$%s$%s", b64.EncodeToString(salt), b64.EncodeToString(argon2.IDKey([]byte("x"), salt, 16, 8, 1, 32))); !checkPassword(ok, "x") {
		t.Fatal("t=16 refused")
	}
	if !checkPassword(dummyHash, "gamify-admin-dummy-password") {
		t.Fatal("dummy hash is malformed: an unknown email would be faster than a wrong password")
	}
}
