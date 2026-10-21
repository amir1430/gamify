package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type TelegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	// LanguageCode is the user's Telegram language (IETF tag, e.g. "en", "pt-br"); may be empty.
	LanguageCode string `json:"language_code"`
	// StartParam is the link's startapp value (t.me/<bot>/<app>?startapp=…), signed with the rest.
	StartParam string `json:"-"`
}

var ErrInvalidInitData = errors.New("auth: invalid initData")

// ValidateInitData checks the initData HMAC against the bot token and rejects data older than maxAge.
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func ValidateInitData(initData, botToken string, maxAge time.Duration, now time.Time) (TelegramUser, error) {
	var u TelegramUser
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return u, ErrInvalidInitData
	}
	gotHash, err := hex.DecodeString(vals.Get("hash"))
	if err != nil || len(gotHash) == 0 {
		return u, ErrInvalidInitData
	}

	pairs := make([]string, 0, len(vals))
	for k := range vals {
		if k != "hash" {
			pairs = append(pairs, k+"="+vals.Get(k))
		}
	}
	slices.Sort(pairs)

	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	if !hmac.Equal(hmacSHA256(secret, []byte(strings.Join(pairs, "\n"))), gotHash) {
		return u, ErrInvalidInitData
	}

	authDate, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || now.Sub(time.Unix(authDate, 0)) > maxAge {
		return u, ErrInvalidInitData
	}
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID == 0 {
		return u, ErrInvalidInitData
	}
	u.StartParam = vals.Get("start_param")
	return u, nil
}

var ErrInvalidLogin = errors.New("auth: invalid login")

// ValidateLogin checks Telegram Login Widget data (the admin site): every field the widget sent,
// as text, including hash. Unlike initData the key is SHA256(bot token), not an HMAC of it.
// https://core.telegram.org/widgets/login#checking-authorization
func ValidateLogin(fields map[string]string, botToken string, maxAge time.Duration, now time.Time) (TelegramUser, error) {
	var u TelegramUser
	gotHash, err := hex.DecodeString(fields["hash"])
	if err != nil || len(gotHash) == 0 {
		return u, ErrInvalidLogin
	}
	pairs := make([]string, 0, len(fields))
	for k, v := range fields {
		if k != "hash" {
			pairs = append(pairs, k+"="+v)
		}
	}
	slices.Sort(pairs)
	secret := sha256.Sum256([]byte(botToken))
	if !hmac.Equal(hmacSHA256(secret[:], []byte(strings.Join(pairs, "\n"))), gotHash) {
		return u, ErrInvalidLogin
	}
	authDate, err := strconv.ParseInt(fields["auth_date"], 10, 64)
	if err != nil || now.Sub(time.Unix(authDate, 0)) > maxAge {
		return u, ErrInvalidLogin
	}
	if u.ID, err = strconv.ParseInt(fields["id"], 10, 64); err != nil || u.ID == 0 {
		return u, ErrInvalidLogin
	}
	u.Username, u.FirstName = fields["username"], fields["first_name"]
	return u, nil
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// AppLink is the Mini App link appURL (https://t.me/<bot>/<app>) opening with startapp, or "" when
// appURL isn't set. A query already on appURL is kept.
func AppLink(appURL, startapp string) string {
	u, err := url.Parse(appURL)
	if appURL == "" || err != nil {
		return ""
	}
	q := u.Query()
	q.Set("startapp", startapp)
	u.RawQuery = q.Encode()
	return u.String()
}
