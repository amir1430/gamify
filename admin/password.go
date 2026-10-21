package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters for new hashes (OWASP minimum). Stored hashes carry their own, so these can
// change without invalidating them.
const (
	argonMemory  = 19456 // KiB
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

var b64 = base64.RawStdEncoding

// hashPassword returns an argon2id PHC string.
func hashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// checkPassword reports whether pw matches the PHC string hash. Any malformed hash is false.
func checkPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false
	}
	m, okM := param(params[0], "m=", 32)
	t, okT := param(params[1], "t=", 32)
	p, okP := param(params[2], "p=", 8)
	if !okM || !okT || !okP || t == 0 || p == 0 {
		return false
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func param(s, prefix string, bits int) (uint64, bool) {
	v, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, bits)
	return n, err == nil
}
