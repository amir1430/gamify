package admin

import (
	"strings"
	"testing"
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
