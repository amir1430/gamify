package admin

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		t    int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		if got := totpCode(secret, v.t/30); got != v.code {
			t.Errorf("t=%d: %s, want %s", v.t, got, v.code)
		}
	}
}

func TestTOTPReplay(t *testing.T) {
	secret := newTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	s := now.Unix() / 30
	step, ok := verifyTOTP(secret, totpCode(secret, s), now, 0)
	if !ok || step != s {
		t.Fatalf("current code: step %d ok %v", step, ok)
	}
	if _, ok := verifyTOTP(secret, totpCode(secret, s), now, s); ok {
		t.Fatal("replayed code accepted")
	}
	if step, ok := verifyTOTP(secret, totpCode(secret, s-1), now, s-2); !ok || step != s-1 {
		t.Fatalf("previous code: step %d ok %v", step, ok)
	}
	if _, ok := verifyTOTP(secret, totpCode(secret, s-1), now, s-1); ok {
		t.Fatal("previous code accepted with last = s-1")
	}
	if step, ok := verifyTOTP(secret, totpCode(secret, s+1), now, s); !ok || step != s+1 {
		t.Fatalf("next code: step %d ok %v", step, ok)
	}
	if _, ok := verifyTOTP(secret, totpCode(secret, s+2), now, 0); ok {
		t.Fatal("code two steps ahead accepted")
	}
	if _, ok := verifyTOTP(secret, totpCode(secret, s-2), now, 0); ok {
		t.Fatal("code two steps behind accepted")
	}
	if _, ok := verifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
}

func TestTOTPSecretAndURI(t *testing.T) {
	a, b := newTOTPSecret(), newTOTPSecret()
	if len(a) != 20 || bytes.Equal(a, b) {
		t.Fatalf("secrets %x %x", a, b)
	}
	u := otpauthURI("My Brand", "a+b@x.io", []byte("12345678901234567890"))
	want := "otpauth://totp/My%20Brand:a%2Bb%40x.io?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=My%20Brand&algorithm=SHA1&digits=6&period=30"
	if u != want {
		t.Fatalf("uri\n%s\nwant\n%s", u, want)
	}
	if strings.Contains(u, "=&") {
		t.Fatal("padding in secret")
	}
}

func TestTOTPSealAAD(t *testing.T) {
	k := totp{key: bytes.Repeat([]byte{7}, 32)}
	secret := newTOTPSecret()
	sealed := k.seal(1, secret)
	got, err := k.open(1, sealed)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("open as 1: %x %v", got, err)
	}
	if _, err := k.open(2, sealed); err == nil {
		t.Fatal("opened as admin 2")
	}
	if _, err := k.open(1, sealed[:5]); err == nil {
		t.Fatal("opened truncated ciphertext")
	}
	if bytes.Equal(k.seal(1, secret), sealed) {
		t.Fatal("nonce reused")
	}
}
