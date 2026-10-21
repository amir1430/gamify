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
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/auth/custom"
	"github.com/amir1430/gamify/auth/telegram"
	"github.com/amir1430/gamify/config"
	"github.com/amir1430/gamify/config/drivers"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/games/dice"
	"github.com/amir1430/gamify/games/dice/diceadmin"
	"github.com/amir1430/gamify/ledger"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		return adminCmd(os.Args[2:])
	}
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
	diceSvc, err := dice.Register(ctx, a, fair)
	if err != nil {
		return err
	}
	acfg, adminOn, err := adminConfig(os.Getenv)
	if err != nil {
		return err
	}
	if adminOn {
		acfg.Fairness = fair
		adm, err := admin.Register(ctx, a, acfg)
		if err != nil {
			return err
		}
		if err := diceadmin.Register(adm, diceSvc); err != nil {
			return err
		}
	}
	return a.Run(ctx)
}

// adminCmd runs `gamify admin add-user <email> <role>`: it opens only the Postgres pool.
func adminCmd(args []string) error {
	if len(args) != 3 || args[0] != "add-user" {
		return errors.New("usage: gamify admin add-user <email> <owner|operator|support|viewer>")
	}
	role, err := parseRole(args[2])
	if err != nil {
		return err
	}
	cfg, err := config.Load(envOr("GAMIFY_CONFIG", "gamify.yaml"))
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	pw, err := admin.CreateUser(ctx, pool, args[1], role)
	if err != nil {
		return err
	}
	fmt.Printf("created %s (%s)\npassword: %s\nlog in to enrol TOTP\n", strings.ToLower(strings.TrimSpace(args[1])), role, pw)
	return nil
}

func parseRole(s string) (admin.Role, error) {
	r := admin.Role(s)
	if !r.Valid() {
		return "", fmt.Errorf("role %q: want owner, operator, support or viewer", s)
	}
	return r, nil
}

// adminConfig reads the console's settings: off (false) unless ADMIN_ADDR is set. Also
// ADMIN_ORIGIN, ADMIN_TOTP_KEY (64 hex chars), ADMIN_ALLOW_IPS (CIDRs) and
// ADMIN_APPROVE_ABOVE ("code:minor-units,…"); TG_BOT_TOKEN enables Telegram login to the console.
func adminConfig(getenv func(string) string) (admin.Config, bool, error) {
	addr := getenv("ADMIN_ADDR")
	if addr == "" {
		return admin.Config{}, false, nil
	}
	cfg := admin.Config{Addr: addr, Origin: getenv("ADMIN_ORIGIN"), TelegramBotToken: getenv("TG_BOT_TOKEN")}
	key, err := hex.DecodeString(getenv("ADMIN_TOTP_KEY"))
	if err != nil || len(key) != 32 {
		return cfg, false, errors.New("ADMIN_TOTP_KEY: want 64 hex chars (openssl rand -hex 32)")
	}
	cfg.TOTPKey = key
	if v := getenv("ADMIN_ALLOW_IPS"); v != "" {
		for _, part := range strings.Split(v, ",") {
			p, err := netip.ParsePrefix(strings.TrimSpace(part))
			if err != nil {
				return cfg, false, fmt.Errorf("ADMIN_ALLOW_IPS: %w", err)
			}
			cfg.AllowIPs = append(cfg.AllowIPs, p)
		}
	}
	if v := getenv("ADMIN_APPROVE_ABOVE"); v != "" {
		cfg.ApproveAbove = map[ledger.Currency]int64{}
		for _, part := range strings.Split(v, ",") {
			code, amt, ok := strings.Cut(strings.TrimSpace(part), ":")
			n, err := strconv.ParseInt(amt, 10, 64)
			if !ok || code == "" || err != nil || n < 0 {
				return cfg, false, fmt.Errorf("ADMIN_APPROVE_ABOVE: %q: want code:minor-units (>= 0)", part)
			}
			cfg.ApproveAbove[ledger.Currency(code)] = n
		}
	}
	return cfg, true, nil
}

// parseCurrencies reads "code:ledger:minbet[:welcome[:decimals]],…", the first being the default.
func parseCurrencies(s string) ([]ledger.CurrencyConfig, error) {
	var out []ledger.CurrencyConfig
	for _, part := range strings.Split(s, ",") {
		f := strings.Split(strings.TrimSpace(part), ":")
		if len(f) < 3 || len(f) > 5 {
			return nil, fmt.Errorf("CURRENCIES: %q: want code:ledger:minbet[:welcome[:decimals]]", part)
		}
		f = append(f, "0", "0")
		lg, err1 := strconv.ParseUint(f[1], 10, 32)
		minBet, err2 := strconv.ParseInt(f[2], 10, 64)
		welcome, err3 := strconv.ParseInt(f[3], 10, 64)
		decimals, err4 := strconv.ParseUint(f[4], 10, 8)
		if err := errors.Join(err1, err2, err3, err4); err != nil {
			return nil, fmt.Errorf("CURRENCIES: %q: %w", part, err)
		}
		if decimals > 18 {
			return nil, fmt.Errorf("CURRENCIES: %q: decimals %d > 18", part, decimals)
		}
		out = append(out, ledger.CurrencyConfig{Code: ledger.Currency(f[0]), Ledger: uint32(lg), MinBet: minBet,
			Welcome: welcome, Decimals: uint8(decimals)})
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
