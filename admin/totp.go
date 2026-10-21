package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TOTP per RFC 6238: SHA-1, 6 digits, 30 s steps.
const (
	totpPeriod = 30
	totpDigits = 6
)

// totp seals TOTP secrets at rest with AES-256-GCM (Config.TOTPKey, 32 bytes).
type totp struct{ key []byte }

// newTOTPSecret returns a fresh 160-bit secret (RFC 4226's recommended length).
func newTOTPSecret() []byte {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return b
}

// totpCode is the 6-digit code for a time step (RFC 4226 HOTP with the step as the counter).
func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// verifyTOTP returns the accepted step, or ok=false (wrong code or step ≤ last). It accepts the
// steps now-1, now and now+1 for clock drift; storing the returned step as the next last refuses
// a replay.
func verifyTOTP(secret []byte, code string, now time.Time, last int64) (step int64, ok bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for s := cur - 1; s <= cur+1; s++ {
		if s <= last {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, s)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// otpauthURI is the authenticator-app setup URI (Key URI Format), shown as a QR code.
func otpauthURI(issuer, email string, secret []byte) string {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	i, e := escape(issuer), escape(email)
	return "otpauth://totp/" + i + ":" + e + "?secret=" + enc + "&issuer=" + i +
		"&algorithm=SHA1&digits=" + strconv.Itoa(totpDigits) + "&period=" + strconv.Itoa(totpPeriod)
}

// escape percent-encodes for both the label and a query value (space as %20, not +).
func escape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func (k totp) aead() cipher.AEAD {
	block, err := aes.NewCipher(k.key)
	if err != nil {
		panic(err) // the key's length is checked when the module is configured
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return gcm
}

func totpAAD(adminID int64) []byte { return []byte("admin-totp:" + strconv.FormatInt(adminID, 10)) }

// seal encrypts a secret for one admin: nonce || ciphertext, bound to the admin's id.
func (k totp) seal(adminID int64, secret []byte) []byte {
	gcm := k.aead()
	nonce := make([]byte, gcm.NonceSize(), gcm.NonceSize()+len(secret)+gcm.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return gcm.Seal(nonce, nonce, secret, totpAAD(adminID))
}

// open decrypts what seal returned for the same admin.
func (k totp) open(adminID int64, sealed []byte) ([]byte, error) {
	gcm := k.aead()
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("admin: sealed TOTP secret too short")
	}
	n := gcm.NonceSize()
	return gcm.Open(nil, sealed[:n], sealed[n:], totpAAD(adminID))
}
