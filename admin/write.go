package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/amir1430/gamify/auth"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5"
)

var (
	errReasonRequired = &apiError{http.StatusBadRequest, "reason_required", "a reason is required"}
	errLoosens        = &apiError{http.StatusBadRequest, "loosens", "the override is above the game's cap"}
	errBadOverride    = &apiError{http.StatusBadRequest, "bad_override", "invalid limit override"}
	errLastIdentity   = &apiError{http.StatusConflict, "last_identity", "the user's only login can't be unlinked"}
)

func (adm *Admin) registerWrites() {
	adm.handle("PUT /api/games/{game}", PermLimits, adm.setGame)
	adm.handle("PUT /api/games/{game}/limits/{currency}/{variant}", PermLimits, adm.setLimit)
	adm.handle("DELETE /api/games/{game}/limits/{currency}/{variant}", PermLimits, adm.deleteLimit)
	adm.handle("DELETE /api/users/{id}/identities/{provider}", PermIdentity, adm.unlink)
	adm.handle("POST /api/users/{id}/disconnect", PermIdentity, adm.disconnect)
}

// reasonBody is the body of the identity writes.
type reasonBody struct {
	Reason string `json:"reason"`
}

// readReason reads {reason}; a missing body, bad JSON aside, or a blank reason is reason_required.
func readReason(w http.ResponseWriter, r *http.Request) (string, error) {
	var b reasonBody
	if r.Body != nil && r.ContentLength != 0 {
		if err := decode(w, r, &b); err != nil {
			return "", err
		}
	}
	b.Reason = strings.TrimSpace(b.Reason)
	if b.Reason == "" {
		return "", errReasonRequired
	}
	return b.Reason, nil
}

// changeView is the audit shape of a game or limit change, snake_case like the API.
func changeView(v any) any {
	switch v := v.(type) {
	case nil:
		return nil
	case bool:
		return map[string]any{"enabled": v}
	case gameconfig.LimitOverride:
		return map[string]any{"enabled": v.Enabled, "min_bet": v.MinBet, "max_bet": v.MaxBet, "max_payout": v.MaxPayout}
	}
	return v
}

func gameErr(err error) error {
	switch {
	case errors.Is(err, gameconfig.ErrUnknownGame):
		return errNotFound
	case errors.Is(err, gameconfig.ErrLoosens):
		return errLoosens
	case errors.Is(err, gameconfig.ErrBadOverride):
		return errBadOverride
	}
	return err
}

// gameWrite runs a gameconfig change and its audit row in one transaction; after the commit it
// reloads the cache and publishes the players' view. A change that changes nothing writes no row.
func (adm *Admin) gameWrite(w http.ResponseWriter, r *http.Request, s *session, action, target string,
	change func(ctx context.Context, tx pgx.Tx) (gameconfig.Change, error), status int) error {
	ctx := r.Context()
	changed := false
	err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		c, err := change(ctx, tx)
		if err != nil {
			return gameErr(err)
		}
		if changed = !(c.Before == nil && c.After == nil) && !reflect.DeepEqual(c.Before, c.After); !changed {
			return nil
		}
		return adm.audit(ctx, tx, s, r, action, target, changeView(c.Before), changeView(c.After), "")
	})
	if err != nil {
		return err
	}
	if changed {
		adm.publishConfig(ctx)
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return nil
	}
	writeJSON(w, status, struct{}{})
	return nil
}

// sideEffectCtx is a context for work after a commit: it outlives the request (a client that
// hangs up must not skip it) but not forever.
func sideEffectCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
}

// kick drops the user's realtime connections, after the audit row committed.
func (adm *Admin) kick(parent context.Context, id int64) error {
	ctx, cancel := sideEffectCtx(parent)
	defer cancel()
	return adm.a.Realtime.Disconnect(ctx, id)
}

// publishConfig reloads the games' cache and tells the players, after a commit. The change is
// stored by then, so a failure here is logged, not an error to the console user.
func (adm *Admin) publishConfig(parent context.Context) {
	ctx, cancel := sideEffectCtx(parent)
	defer cancel()
	if err := adm.a.Games.Load(ctx); err != nil {
		slog.ErrorContext(ctx, "admin: reload game config", "err", err)
	}
	body := map[string]any{"games": adm.a.Games.Public(), "off": adm.a.Games.OffCurrencies(), "currencies": adm.a.Ledger.Currencies()}
	if err := realtime.PublishJSON(ctx, adm.a.Realtime, gameconfig.Channel, body); err != nil {
		slog.ErrorContext(ctx, "admin: publish game config", "err", err)
	}
}

func (adm *Admin) setGame(w http.ResponseWriter, r *http.Request, s *session) error {
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	if b.Enabled == nil {
		return badRequest("enabled is required")
	}
	game := r.PathValue("game")
	return adm.gameWrite(w, r, s, "game.set", "game:"+game, func(ctx context.Context, tx pgx.Tx) (gameconfig.Change, error) {
		return adm.a.Games.SetGame(ctx, tx, game, *b.Enabled)
	}, http.StatusOK)
}

// limitKey reads the game, currency and variant of a limit route; variant "_" is "".
func limitKey(r *http.Request) (game, currency, variant, target string) {
	game, currency, variant = r.PathValue("game"), r.PathValue("currency"), r.PathValue("variant")
	if variant == "_" {
		variant = ""
	}
	return game, currency, variant, "game:" + game + "/" + currency + "/" + variant
}

func (adm *Admin) setLimit(w http.ResponseWriter, r *http.Request, s *session) error {
	var b struct {
		Enabled   *bool  `json:"enabled"`
		MinBet    *int64 `json:"min_bet"`
		MaxBet    *int64 `json:"max_bet"`
		MaxPayout *int64 `json:"max_payout"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	if b.Enabled == nil {
		return badRequest("enabled is required")
	}
	game, currency, variant, target := limitKey(r)
	o := gameconfig.LimitOverride{Enabled: *b.Enabled, MinBet: b.MinBet, MaxBet: b.MaxBet, MaxPayout: b.MaxPayout}
	return adm.gameWrite(w, r, s, "game.limit.set", target, func(ctx context.Context, tx pgx.Tx) (gameconfig.Change, error) {
		return adm.a.Games.SetLimit(ctx, tx, game, currency, variant, o)
	}, http.StatusOK)
}

func (adm *Admin) deleteLimit(w http.ResponseWriter, r *http.Request, s *session) error {
	game, currency, variant, target := limitKey(r)
	return adm.gameWrite(w, r, s, "game.limit.delete", target, func(ctx context.Context, tx pgx.Tx) (gameconfig.Change, error) {
		return adm.a.Games.DeleteLimit(ctx, tx, game, currency, variant)
	}, http.StatusNoContent)
}

func (adm *Admin) unlink(w http.ResponseWriter, r *http.Request, s *session) error {
	id, err := userID(r)
	if err != nil {
		return err
	}
	reason, err := readReason(w, r)
	if err != nil {
		return err
	}
	provider := r.PathValue("provider")
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		if err := adm.a.Users.UnlinkTx(ctx, tx, id, provider); err != nil {
			switch {
			case errors.Is(err, auth.ErrNoIdentity):
				return errNotFound
			case errors.Is(err, auth.ErrLastIdentity):
				return errLastIdentity
			}
			return err
		}
		return adm.audit(ctx, tx, s, r, "user.unlink", userTarget(id), map[string]string{"provider": provider}, nil, reason)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (adm *Admin) disconnect(w http.ResponseWriter, r *http.Request, s *session) error {
	id, err := userID(r)
	if err != nil {
		return err
	}
	reason, err := readReason(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if users, err := adm.a.Users.Get(ctx, id); err != nil {
		return err
	} else if len(users) == 0 {
		return errNotFound
	}
	if err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		return adm.audit(ctx, tx, s, r, "user.disconnect", userTarget(id), nil, nil, reason)
	}); err != nil {
		return err
	}
	if err := adm.kick(ctx, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func userTarget(id int64) string { return "user:" + strconv.FormatInt(id, 10) }
