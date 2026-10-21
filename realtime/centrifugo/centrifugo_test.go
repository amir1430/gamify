package centrifugo

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToken(t *testing.T) {
	tr := &Transport{cfg: Config{TokenSecret: []byte("s3cret"), TokenTTL: time.Hour}}
	now := time.Unix(1_700_000_000, 0)
	tok := tr.Token(42, "Ali", now)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q", tok)
	}
	m := hmac.New(sha256.New, []byte("s3cret"))
	m.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(m.Sum(nil)) != parts[2] {
		t.Fatal("bad signature")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Sub      string            `json:"sub"`
		Exp      int64             `json:"exp"`
		Channels []string          `json:"channels"`
		Info     map[string]string `json:"info"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Sub != "42" || c.Exp != now.Add(time.Hour).Unix() || strings.Join(c.Channels, ",") != "user:42,online:all" || c.Info["name"] != "Ali" {
		t.Fatalf("claims %+v", c)
	}
}
