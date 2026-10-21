package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/amir1430/gamify/auth"
)

func TestValidateInitData(t *testing.T) {
	const bot = "123:ABC"
	now := time.Unix(1_700_000_100, 0)
	user := `{"id":777,"username":"alice","language_code":"pt-br"}`
	dcs := "auth_date=1700000000\nquery_id=Q1\nuser=" + user
	hash := hex.EncodeToString(hmacSHA256(hmacSHA256([]byte("WebAppData"), []byte(bot)), []byte(dcs)))
	initData := url.Values{"auth_date": {"1700000000"}, "query_id": {"Q1"}, "user": {user}, "hash": {hash}}.Encode()

	u, err := ValidateInitData(initData, bot, time.Hour, now)
	if err != nil || u.ID != 777 || u.LanguageCode != "pt-br" {
		t.Fatalf("valid data rejected: %v %+v", err, u)
	}
	if _, err := ValidateInitData(initData, "other:bot", time.Hour, now); err == nil {
		t.Fatal("wrong bot token accepted")
	}
	if _, err := ValidateInitData(initData, bot, time.Minute, now); err == nil {
		t.Fatal("stale data accepted")
	}
	// start_param is signed with the rest.
	dcs = "auth_date=1700000000\nstart_param=r_abc\nuser=" + user
	hash2 := hex.EncodeToString(hmacSHA256(hmacSHA256([]byte("WebAppData"), []byte(bot)), []byte(dcs)))
	withStart := url.Values{"auth_date": {"1700000000"}, "start_param": {"r_abc"}, "user": {user}, "hash": {hash2}}
	if u, err := ValidateInitData(withStart.Encode(), bot, time.Hour, now); err != nil || u.StartParam != "r_abc" {
		t.Fatalf("start_param: %v %+v", err, u)
	}
	withStart.Set("start_param", "r_xyz")
	if _, err := ValidateInitData(withStart.Encode(), bot, time.Hour, now); err == nil {
		t.Fatal("changed start_param accepted")
	}
	tampered := url.Values{"auth_date": {"1700000000"}, "query_id": {"Q1"}, "user": {`{"id":1}`}, "hash": {hash}}.Encode()
	if _, err := ValidateInitData(tampered, bot, time.Hour, now); err == nil {
		t.Fatal("tampered data accepted")
	}
}

func TestValidateLogin(t *testing.T) {
	const bot = "123:ABC"
	now := time.Unix(1_700_000_100, 0)
	key := sha256.Sum256([]byte(bot))
	dcs := "auth_date=1700000000\nfirst_name=Amir\nid=777\nusername=amir"
	fields := map[string]string{"auth_date": "1700000000", "first_name": "Amir", "id": "777", "username": "amir",
		"hash": hex.EncodeToString(hmacSHA256(key[:], []byte(dcs)))}

	u, err := ValidateLogin(fields, bot, time.Hour, now)
	if err != nil || u.ID != 777 || u.FirstName != "Amir" || u.Username != "amir" {
		t.Fatalf("valid login rejected: %v %+v", err, u)
	}
	if _, err := ValidateLogin(fields, "other:bot", time.Hour, now); err == nil {
		t.Fatal("wrong bot token accepted")
	}
	if _, err := ValidateLogin(fields, bot, time.Minute, now); err == nil {
		t.Fatal("stale login accepted")
	}
	fields["id"] = "1"
	if _, err := ValidateLogin(fields, bot, time.Hour, now); err == nil {
		t.Fatal("tampered login accepted")
	}
}

func TestAppLink(t *testing.T) {
	for _, c := range []struct{ app, want string }{
		{"https://t.me/bot/app", "https://t.me/bot/app?startapp=r_abc"},
		{"https://t.me/bot?x=1", "https://t.me/bot?startapp=r_abc&x=1"},
		{"", ""},
	} {
		if got := AppLink(c.app, "r_abc"); got != c.want {
			t.Errorf("AppLink(%q) = %q, want %q", c.app, got, c.want)
		}
	}
}

func TestProvider(t *testing.T) {
	const bot = "123:ABC"
	now := time.Unix(1_700_000_100, 0)
	user := `{"id":777,"username":"alice","first_name":"Alice","language_code":"pt-br"}`
	dcs := "auth_date=1700000000\nstart_param=r_abc\nuser=" + user
	hash := hex.EncodeToString(hmacSHA256(hmacSHA256([]byte("WebAppData"), []byte(bot)), []byte(dcs)))
	initData := url.Values{"auth_date": {"1700000000"}, "start_param": {"r_abc"}, "user": {user}, "hash": {hash}}.Encode()
	cred, _ := json.Marshal(map[string]string{"init_data": initData})

	p := New(bot)
	if p.Name() != "telegram" {
		t.Fatalf("name = %q", p.Name())
	}
	id, err := p.Verify(context.Background(), cred, now)
	if err != nil {
		t.Fatal(err)
	}
	want := auth.Identity{Provider: "telegram", Subject: "777", Username: "alice", DisplayName: "Alice", Lang: "pt-br",
		Meta: map[string]string{"start_param": "r_abc"}}
	if !reflect.DeepEqual(id, want) {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
	for _, bad := range []string{`{"init_data":"x"}`, `{}`, `not json`, `{"init_data":` + strconv.Quote(initData) + `,"extra":1}`,
		`{"init_data":` + strconv.Quote(initData) + `} {}`} {
		if _, err := p.Verify(context.Background(), json.RawMessage(bad), now); !errors.Is(err, auth.ErrInvalidCredential) {
			t.Errorf("%s: err = %v, want ErrInvalidCredential", bad, err)
		}
	}
	if _, err := p.Verify(context.Background(), cred, now.Add(25*time.Hour)); !errors.Is(err, auth.ErrInvalidCredential) {
		t.Fatalf("day-old initData: err = %v", err)
	}
}
