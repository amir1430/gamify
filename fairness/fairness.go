// Package fairness owns seed pairs and turns (server seed, client seed, nonce) into randomness.
// Server seeds are secret until revealed; see CLAUDE.md "Seed secrecy".
package fairness

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The scheme matches the public verifier at https://game-verify.github.io/public/{game}.html:
// the server seed is shown as 64 hex characters, and that *text* is the HMAC key
// (CryptoJS.HmacSHA256(msg, serverSeed) with a string key = its UTF-8 bytes).
// The commitment is sha256 of the same text. Changing any of this breaks every published
// verification; cross-checked against their scripts in fairness_test.go.

// NewServerSeed returns 32 fresh random bytes and the public commitment sha256(Key(seed)).
func NewServerSeed() (seed, hash []byte) {
	seed = make([]byte, 32)
	rand.Read(seed) // never returns an error since Go 1.24
	h := sha256.Sum256(Key(seed))
	return seed, h[:]
}

// Key is the HMAC key for a server seed: its hex text (what the player sees once revealed).
func Key(seed []byte) []byte { return []byte(hex.EncodeToString(seed)) }

// NewClientSeed returns a random default client seed.
func NewClientSeed() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// HMACHex is hex(HMAC_SHA256(key, msg)).
func HMACHex(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// Hash is the per-bet hash: HMACHex(key, "clientSeed:nonce") (Dice, Limbo, Mines).
func Hash(key []byte, clientSeed string, nonce int64) string {
	return HMACHex(key, clientSeed+":"+strconv.FormatInt(nonce, 10))
}

// Hash512 is hex(HMAC_SHA512(key, "clientSeed:nonce")) (Plinko, as plinko.html).
func Hash512(key []byte, clientSeed string, nonce int64) string {
	m := hmac.New(sha512.New, key)
	m.Write([]byte(clientSeed + ":" + strconv.FormatInt(nonce, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

// RoundHash is the hash of one round of a multi-round bet: HMACHex(key, "clientSeed:nonce:round") (Hi-Lo).
func RoundHash(key []byte, clientSeed string, nonce int64, round int) string {
	return HMACHex(key, clientSeed+":"+strconv.FormatInt(nonce, 10)+":"+strconv.Itoa(round))
}

// SlideWindow mirrors the verifier's slideWindowNumber: the first 15 hex chars mod 47 pick an
// offset, the 14 hex chars there (56 bits) >> 3 give 53 bits, scaled to [0, events).
// Same float64 operations in the same order as the JavaScript, so results match bit for bit.
func SlideWindow(hash string, events float64) float64 {
	head, _ := strconv.ParseUint(hash[:15], 16, 64)
	i := int(head % 47)
	win, _ := strconv.ParseUint(hash[i:i+14], 16, 64)
	return float64(win>>3) * 0x1p-53 * events
}

// Float4 is the verifier's "bytes to number" on chunk i of a hex hash (Coinflip, Tower, Wheel):
// the 4 bytes at hex offset 8i become b0/256 + b1/256² + b2/256³ + b3/256⁴, which is exactly uint32/2³².
func Float4(hash string, i int) float64 {
	v, _ := strconv.ParseUint(hash[8*i:8*i+8], 16, 32)
	return float64(v) / (1 << 32)
}

// Floats returns n uniform values in [0,1) from HMAC_SHA256(key, "clientSeed:nonce:cursor"),
// 4 bytes per float. Used by Keno, which the public verifier doesn't cover.
func Floats(key []byte, clientSeed string, nonce uint64, n int) []float64 {
	out := make([]float64, 0, n)
	for cursor := 0; len(out) < n; cursor++ {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(clientSeed + ":" + strconv.FormatUint(nonce, 10) + ":" + strconv.Itoa(cursor)))
		b := m.Sum(nil)
		for i := 0; i+4 <= len(b) && len(out) < n; i += 4 {
			out = append(out, float64(binary.BigEndian.Uint32(b[i:]))/(1<<32))
		}
	}
	return out
}

// Sealer encrypts server seeds at rest with AES-256-GCM.
// The user ID is bound as additional data so a sealed seed can't be moved to another user.
type Sealer struct {
	KeyID string
	aead  cipher.AEAD
}

func NewSealer(keyID string, key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("fairness: seed key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{KeyID: keyID, aead: aead}, nil
}

func (s *Sealer) Seal(userID int64, seed []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, seed, binary.BigEndian.AppendUint64(nil, uint64(userID)))
}

func (s *Sealer) Open(userID int64, keyID string, sealed []byte) ([]byte, error) {
	if keyID != s.KeyID {
		return nil, fmt.Errorf("fairness: unknown seed key %q", keyID)
	}
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("fairness: sealed seed too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], binary.BigEndian.AppendUint64(nil, uint64(userID)))
}

// A pot's result hash is HMAC(seed text, "potID:seed1:seed2:…") with the entries' client seeds
// in entry order; per-round hashes append ":round".
func potMsg(potID int64, clientSeeds []string) string {
	return strconv.FormatInt(potID, 10) + ":" + strings.Join(clientSeeds, ":")
}

func PotHash(seedHex string, potID int64, clientSeeds []string) string {
	return HMACHex([]byte(seedHex), potMsg(potID, clientSeeds))
}

func PotRoundHash(seedHex string, potID int64, clientSeeds []string, round int) string {
	return HMACHex([]byte(seedHex), potMsg(potID, clientSeeds)+":"+strconv.Itoa(round))
}

// PotRoundHash512 is the SHA-512 variant (Battles' Plinko paths).
func PotRoundHash512(seedHex string, potID int64, clientSeeds []string, round int) string {
	m := hmac.New(sha512.New, []byte(seedHex))
	m.Write([]byte(potMsg(potID, clientSeeds) + ":" + strconv.Itoa(round)))
	return hex.EncodeToString(m.Sum(nil))
}

// PotFloats is n uniform values in [0,1) for round `round` of a pot: Float4 chunks of
// HMACHex(seed text, "<pot msg>:<round>:<cursor>"), cursor from 0, 8 per hash (Battles' Keno and
// Mines draws, the duels' cards).
func PotFloats(seedHex string, potID int64, clientSeeds []string, round, n int) []float64 {
	out := make([]float64, 0, n)
	for cursor := 0; len(out) < n; cursor++ {
		h := HMACHex([]byte(seedHex), potMsg(potID, clientSeeds)+":"+strconv.Itoa(round)+":"+strconv.Itoa(cursor))
		for i := 0; i < 8 && len(out) < n; i++ {
			out = append(out, Float4(h, i))
		}
	}
	return out
}
