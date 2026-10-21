package fairness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Published test vector. Must never change: the public verifier depends on it.
// Cross-checked: printf 'player-seed:1:0' | openssl dgst -sha256 -hmac 0123456789abcdef0123456789abcdef
var vectorSeed = []byte("0123456789abcdef0123456789abcdef")

func TestFloatsVector(t *testing.T) {
	want := []float64{0.67121221218258142, 0.94435388944111764, 0.87417132267728448}
	got := Floats(vectorSeed, "player-seed", 1, 3)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("float %d = %.17g, want %.17g", i, got[i], want[i])
		}
	}
	// More than 8 floats must roll over to cursor 1 without repeating cursor 0.
	if long := Floats(vectorSeed, "player-seed", 1, 9); long[8] == long[0] {
		t.Fatal("cursor did not advance")
	}
}

func TestSealer(t *testing.T) {
	s, err := NewSealer("k1", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := NewServerSeed()
	sealed := s.Seal(42, seed)
	if got, err := s.Open(42, "k1", sealed); err != nil || !bytes.Equal(got, seed) {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := s.Open(43, "k1", sealed); err == nil {
		t.Fatal("seed opened for the wrong user")
	}
}

// Reference values produced by running the scripts of https://game-verify.github.io/public/
// (dice.html, limbo.html, mines.html, hilo.html + lib/utils.js) in Node with crypto-js.
// Shared by the game packages' tests and the client's test/fair.test.ts.
const (
	RefServerSeed = "1f3a9c2e4b6d8f00112233445566778899aabbccddeeff00a1b2c3d4e5f60718"
	RefClientSeed = "player-seed"
)

func TestPublicVerifierScheme(t *testing.T) {
	seed, _ := hex.DecodeString(RefServerSeed)
	key := Key(seed)
	if string(key) != RefServerSeed {
		t.Fatal("key must be the seed's hex text")
	}
	if h := sha256.Sum256(key); hex.EncodeToString(h[:]) != "e601b7e4a60bcf68df69010fabf53edeb5acf7f38e8a6061024424ddc0f1383a" {
		t.Fatal("commitment differs from the verifier's sha256(server_seed)")
	}
	if got := Hash(key, RefClientSeed, 1); got != "7c0a7adc696e21bcf93f114b9cf30fbd6a019875ab39c2c0d248dc2e7b64ab7e" {
		t.Fatalf("hash = %s", got)
	}
	for nonce, want := range map[int64]int{1: 5287, 2: 1779, 3: 1170} { // dice.html
		if got := int(SlideWindow(Hash(key, RefClientSeed, nonce), 10000)); got != want {
			t.Fatalf("slide window nonce %d = %d, want %d", nonce, got, want)
		}
	}
	if got := RoundHash(key, RefClientSeed, 7, 2); got != HMACHex(key, "player-seed:7:2") {
		t.Fatal("round hash message")
	}
}

func TestPotHash(t *testing.T) {
	ab := []string{"alice", "bob"}
	for _, c := range []struct{ got, want string }{
		{PotHash(RefServerSeed, 1, ab), "501baf1db874fa5024b722c072669fe987b85b77a115f867e76dcee9ac0797dd"},
		{PotHash(RefServerSeed, 7, []string{"alice", "bob", "carol"}), "627add62bd67253221021ecaf0622efbb2d4011c841966364887be93c977c183"},
		{PotRoundHash(RefServerSeed, 1, ab, 0), "4ac6ee0270a81055e9afcb545b87255aeb7657ec9085aa2d4be7655871b6cbe5"},
		{PotRoundHash(RefServerSeed, 1, ab, 1), "e160415f5075d53919151257cef73a67737f7f9b07b7f6a2c9a81a8b0da66681"},
	} {
		if c.got != c.want {
			t.Fatalf("got %s, want %s", c.got, c.want)
		}
	}
	if len(PotRoundHash512(RefServerSeed, 1, ab, 0)) != 128 {
		t.Fatal("512 hash must be 128 hex chars")
	}
}

func TestPotSeed(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	sl, _ := NewSealer("k1", key)
	s := &Service{sealer: sl}
	p := s.NewPotSeed()
	seed, err := s.OpenPotSeed(p.Enc, p.KeyID)
	if err != nil || len(seed) != 64 {
		t.Fatalf("open: %q %v", seed, err)
	}
	if h := sha256.Sum256([]byte(seed)); hex.EncodeToString(h[:]) != p.Commitment {
		t.Fatal("commitment is not sha256 of the seed text")
	}
}
