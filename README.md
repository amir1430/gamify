# gamify

A white-label game platform for Telegram Mini Apps, extracted from the casino's core. A brand picks
its drivers, configures its currencies and registers games. The platform does login, money,
provable fairness, game limits, realtime and ops.

```
module github.com/amir1430/gamify
```

## Layout

| Package | What |
|---|---|
| `app/` | Wiring: migrations, ledger init, login (`POST /auth/{provider}` → realtime token, `auth.link`), `/healthz` `/readyz`, core RPCs (`wallet.*`, `games.config`), reconcile loop, `Run` |
| `ledger/` | All money logic (instant bets, held bets, extra holds, PvP pots and pools, deposits, rewards, swaps, play-only welcome grants), written once on `ledger.Driver` |
| `ledger/tigerbeetle/` | Driver: TigerBeetle 0.16.46 (client version must equal the server's) |
| `ledger/postgres/` | Driver: double-entry Postgres with TigerBeetle's semantics, for brands without TigerBeetle |
| `ledger/ledgertest/` | Conformance suite every ledger driver must pass |
| `realtime/` | `Transport` interface + transport-agnostic RPC `Router` (`Handle`, `JSON`, `ClientError`, error codes) |
| `realtime/centrifugo/` | Driver: Centrifugo v6 (gRPC server API out, gRPC RPC proxy in, HS256 connection tokens) |
| `auth/` | `Provider` interface (credential → `Identity`), users and their linked identities (`user_identities`, one per provider); `Users.Unlink` for admins, never a player RPC |
| `auth/telegram/` | Provider: Mini App initData. Also Login Widget validation, Mini App links, Bot API helpers, `ChatID` |
| `auth/custom/` | Provider: a brand-signed HS256 JWT (`sub`, `aud`, `exp` ≤ 15 min ahead), for brands with their own accounts |
| `fairness/` | Opt-in module for games of chance (`Register`: `seed_pairs` table, `seed.info`, `seed.rotate`): seed pairs sealed with AES-256-GCM, nonces, verifier-compatible hashes and floats, pot seeds |
| `admin/` | Opt-in back office (own migrations, goose table `goose_admin`): console users with roles and permissions, argon2id passwords, TOTP, audit log, balance adjustments; `CreateUser` for the first owner |
| `gameconfig/` | Admin-set limits per game × currency × variant (code defaults are hard caps) |
| `metrics/` | Prometheus metrics and request logs |
| `db/` | Platform migrations + `Migrate` (each module migrates with its own goose table) |
| `games/dice/` | Sample game: shows the plug-in contract (`Register`); `apptest_test.go` shows how to test one |
| `apptest/` | A real `App` for tests: fresh Postgres database per test, Postgres ledger, in-memory transport; `User`, `Fund`, `Call`, `Pushes`, `ReconcileNow` |
| `config/` | `gamify.yaml`: infra settings (Postgres, ledger driver, Centrifugo, addresses), secrets as `${VAR}`, Centrifugo's `config.json` generated from it. No cgo |
| `config/drivers/` | Builds the pool, ledger driver and Centrifugo transport a `config.Config` describes |
| `cmd/gamify/` | Sample brand binary: infra from `gamify.yaml`, product settings from env |
| `cmd/gamify-config/` | Checks a `gamify.yaml` and generates Centrifugo's `config.json` from it |

## Drivers

```go
// ledger.Driver: TigerBeetle's semantics. Linked chains, pending/post/void, balancing debits,
// no-overdraft accounts, idempotent IDs, failed IDs reserved.
type Driver interface {
	CreateAccounts(ctx, []Account) error
	CreateTransfers(ctx, []Transfer) ([]TransferResult, error)
	LookupAccounts(ctx, []ID) ([]AccountBalance, error)
}

// realtime.Transport
type Transport interface {
	Publish(ctx, channel string, data []byte) error
	Disconnect(ctx, userID int64) error
	Online(ctx) (map[int64]int, error)
	Token(userID int64, name string, now time.Time) string
	Serve(ctx, *Router) error
	Ping(ctx) error
}
```

To add a ledger driver, implement `ledger.Driver` and call `ledgertest.Run` from its test. It has
to pass the same suite as TigerBeetle: that suite is the definition of correct. The Postgres driver
is the worked example.

To add a realtime driver, implement `realtime.Transport`. Client RPCs go to `Router.Call(ctx,
method, user, data)`, where `user` is the transport's authenticated user, never a payload field.

## Adding a game

A game is a package with its own migrations (`migrations/*.sql`, own goose table), sqlc queries
and a `Register(ctx, *app.App, …)` that migrates, builds its service, registers RPC methods
(`a.Router.Handle`) and its reconciler (`a.Reconcile`). A game of chance also takes the
`*fairness.Service` that `fairness.Register` returns and, if it's multi-step, adds a seed rotation
guard (`fair.Guard`); its sqlc schema lists `fairness/migrations` too. It claims a `ledger.Game`
ID with `a.Ledger.RegisterGame` (platform games below `ledger.FirstBrandGame`, a brand's own
`FirstBrandGame..MaxGame`); the ID never changes. See `games/dice` and, for brands,
[docs/writing-a-game.md](docs/writing-a-game.md).

## Versioning

Semver. Brands depend on tagged versions (`go get github.com/amir1430/gamify@v1.x.y`); migrations
run on start, so an upgrade is a `go get` and a deploy. Within a major version exported APIs don't
break and committed migrations never change (new ones are added). Generated query code is in
`internal/store` packages and isn't public API. [CHANGELOG.md](CHANGELOG.md) lists every release
with upgrade notes.

## Currencies

Configured per brand: `ledger.CurrencyConfig{Code, Ledger, MinBet, Welcome, Decimals}`. The first
currency is the default. `Welcome` > 0 grants that much once per user and keeps it play-only: sales and PvP
entries can't take the balance below it. The sample binary reads
`CURRENCIES=usd:1:1,tickets:2:10:1000` (code:ledger:minbet[:welcome[:decimals]]; `Decimals` is
display only, ≤ 18).

ID layout, the same on every driver: user account `(ledger, user)`, system account
`(0, ledger<<8 | kind)`, bet transfer `(game<<8 | leg, bet)`, others `(ns<<32 …, row)`. The
casino's TigerBeetle data uses fixed system account IDs and other namespaces, so moving the casino
onto this platform needs a balance migration (or a compat layout); it can't reuse its cluster as-is.

## Commands

```sh
sqlc generate                    # after editing queries or migrations
go test ./...
# integration: real Postgres (+ TigerBeetle), skipped otherwise
TEST_DATABASE_URL=postgres://... TEST_TB_ADDRESS=127.0.0.1:3000 go test ./ledger/... ./games/... -run Integration
e2e/run.sh                       # fresh Postgres + TigerBeetle + Centrifugo in Docker; plays over the real path, per ledger driver
```

Run the sample: `cp .env.example .env` (fill in the secrets), then `make up`. It copies
`gamify.example.yaml` to `gamify.yaml` once, generates `infra/centrifugo/config.json` from it and
starts Postgres, Centrifugo and the app in Docker Compose (`make up PROFILE=tigerbeetle` adds
TigerBeetle; set `ledger.driver` to match). Infra lives in `gamify.yaml` (`GAMIFY_CONFIG` names
another file); secrets are `${VAR}` references, never values. The sample's product settings stay
env: `SEED_KEY` (`openssl rand -hex 32`), login providers `TG_BOT_TOKEN` (Telegram) and/or
`AUTH_JWT_SECRET` (custom JWT, ≥ 32 bytes, with `AUTH_JWT_AUDIENCE`), at least one, optional `SEED_KEY_ID` `CURRENCIES`
`DEV_FAUCET`. `make check` validates `gamify.yaml`. Centrifugo's generated config sends every RPC to
the proxy (`without_namespace`) and never holds a secret: Centrifugo reads `CENTRIFUGO_GRPC_API_KEY`
and `CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY` from its env.

macOS: export `CGO_LDFLAGS=-Wl,-ld_classic` before building (TigerBeetle's static lib vs Apple's new linker).

Regenerate the Centrifugo protos (only when bumping Centrifugo):
```sh
protoc -I proto/centrifugo --go_out=realtime/centrifugo/apiproto --go_opt=paths=source_relative --go-grpc_out=realtime/centrifugo/apiproto --go-grpc_opt=paths=source_relative api.proto
protoc -I proto/centrifugo --go_out=realtime/centrifugo/proxyproto --go_opt=paths=source_relative --go-grpc_out=realtime/centrifugo/proxyproto --go-grpc_opt=paths=source_relative proxy.proto
```

## Not moved yet (still casino-only)

feed and leaderboards, player restrictions, rewards and referrals, swaps (service), deposits
(wallet provider), profile stats, share cards, the admin API, the Crash hash chain, and the other games.
