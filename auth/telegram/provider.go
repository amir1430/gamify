package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/amir1430/gamify/auth"
)

// MaxAge is how long a Mini App's initData logs in: the client renews its session by sending it
// again, so it must outlive the realtime token.
const MaxAge = 24 * time.Hour

// Provider logs in Mini App users: the credential is {"init_data": "<Telegram.WebApp.initData>"},
// signed with the bot's token.
type Provider struct{ botToken string }

var _ auth.Provider = (*Provider)(nil)

func New(botToken string) *Provider { return &Provider{botToken: botToken} }

func (*Provider) Name() string { return "telegram" }

func (p *Provider) Verify(_ context.Context, credential json.RawMessage, now time.Time) (auth.Identity, error) {
	var c struct {
		InitData string `json:"init_data"`
	}
	dec := json.NewDecoder(bytes.NewReader(credential))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil || c.InitData == "" || dec.More() {
		return auth.Identity{}, fmt.Errorf("%w: want {\"init_data\": …}", auth.ErrInvalidCredential)
	}
	u, err := ValidateInitData(c.InitData, p.botToken, MaxAge, now)
	if err != nil {
		return auth.Identity{}, fmt.Errorf("%w: %w", auth.ErrInvalidCredential, err)
	}
	id := auth.Identity{Provider: p.Name(), Subject: strconv.FormatInt(u.ID, 10), Username: u.Username,
		DisplayName: u.FirstName, Lang: u.LanguageCode}
	if u.StartParam != "" {
		id.Meta = map[string]string{"start_param": u.StartParam}
	}
	return id, nil
}

// ChatID is the user's Telegram ID, the chat the bot writes to, or auth.ErrNoIdentity for a user
// who never logged in with Telegram.
func ChatID(ctx context.Context, users *auth.Users, userID int64) (int64, error) {
	s, err := users.Subject(ctx, userID, "telegram")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}
