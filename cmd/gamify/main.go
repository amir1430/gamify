// Command gamify is a sample brand: the platform with Dice. Infra comes from gamify.yaml
// (GAMIFY_CONFIG), product settings and keys from env. A brand copies this file and registers its
// own games.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/auth/custom"
	"github.com/amir1430/gamify/auth/telegram"
	"github.com/amir1430/gamify/config"
	"github.com/amir1430/gamify/config/drivers"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/games/dice"
	"github.com/amir1430/gamify/ledger"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(envOr("GAMIFY_CONFIG", "gamify.yaml"))
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Log.Level})))
	d, closeDrivers, err := drivers.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDrivers()

	// Seed encryption key. Never in Postgres, never in the repo.
	seedKey, err := hex.DecodeString(env("SEED_KEY"))
	if err != nil {
		return err
	}
	sealer, err := fairness.NewSealer(envOr("SEED_KEY_ID", "k1"), seedKey)
	if err != nil {
		return err
	}
	currencies, err := parseCurrencies(envOr("CURRENCIES", "usd:1:1,tickets:2:10:1000"))
	if err != nil {
		return err
	}

	// Login: Telegram when TG_BOT_TOKEN is set, a brand-signed JWT (aud AUTH_JWT_AUDIENCE) when
	// AUTH_JWT_SECRET is; at least one.
	if token := os.Getenv("TG_BOT_TOKEN"); token != "" {
		d.Auth = append(d.Auth, telegram.New(token))
	}
	if secret := os.Getenv("AUTH_JWT_SECRET"); secret != "" {
		p, err := custom.New(custom.Config{Secret: []byte(secret), Audience: env("AUTH_JWT_AUDIENCE")})
		if err != nil {
			return err
		}
		d.Auth = append(d.Auth, p)
	}

	ac := cfg.App()
	ac.Currencies, ac.DevFaucet = currencies, os.Getenv("DEV_FAUCET") == "true"
	a, err := app.New(ctx, ac, d)
	if err != nil {
		return err
	}
	fair, err := fairness.Register(ctx, a, sealer)
	if err != nil {
		return err
	}
	if _, err := dice.Register(ctx, a, fair); err != nil {
		return err
	}
	return a.Run(ctx)
}

// parseCurrencies reads "code:ledger:minbet[:welcome],…", the first being the default.
func parseCurrencies(s string) ([]ledger.CurrencyConfig, error) {
	var out []ledger.CurrencyConfig
	for _, part := range strings.Split(s, ",") {
		f := strings.Split(strings.TrimSpace(part), ":")
		if len(f) < 3 || len(f) > 4 {
			return nil, fmt.Errorf("CURRENCIES: %q: want code:ledger:minbet[:welcome]", part)
		}
		f = append(f, "0")
		lg, err1 := strconv.ParseUint(f[1], 10, 32)
		minBet, err2 := strconv.ParseInt(f[2], 10, 64)
		welcome, err3 := strconv.ParseInt(f[3], 10, 64)
		if err := errors.Join(err1, err2, err3); err != nil {
			return nil, fmt.Errorf("CURRENCIES: %q: %w", part, err)
		}
		out = append(out, ledger.CurrencyConfig{Code: ledger.Currency(f[0]), Ledger: uint32(lg), MinBet: minBet, Welcome: welcome})
	}
	return out, nil
}

func env(k string) string {
	v := os.Getenv(k)
	if v == "" {
		slog.Error("missing env", "key", k)
		os.Exit(1)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
