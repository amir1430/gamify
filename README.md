# gamify

A white-label game platform for Telegram Mini Apps, for any kind of game: casino, battle-and-build,
endless runners, puzzles. A brand imports gamify as a Go library, picks its drivers, configures its
currencies and registers its own games. The platform does the parts every game needs:

- **Login**: Telegram Mini App initData or a brand-signed JWT; one user, many linked logins.
- **Money**: a double-entry ledger on TigerBeetle or Postgres with TigerBeetle's semantics. Integer
  minor units, deterministic transfer IDs, overdrafts refused by the driver.
- **Realtime**: RPCs and pushes over Centrifugo.
- **Randomness**: opt-in provable fairness (seed pairs, nonces, verifiable floats) for games of chance.
- **Limits**: per game, currency and variant, set by admins without a deploy.
- **Admin console**: players, balances, adjustments with approval, game limits, audit log, 2FA.
- **Ops**: health checks, Prometheus metrics, a reconcile loop for operations a crash left pending.

It holds no game logic: each game brings its own rules and decides what random numbers mean.

```
module github.com/amir1430/gamify
```

## Contents

- [Quick start: run the sample](#quick-start-run-the-sample)
- [Build a brand with your own games](#build-a-brand-with-your-own-games)
- [Layout](#layout)
- [Money](#money)
- [Configuration](#configuration)
- [Admin console](#admin-console)
- [Development](#development)
- [Versioning](#versioning)
- [Not in the platform yet](#not-in-the-platform-yet)

## Quick start: run the sample

`cmd/gamify` is a sample brand with no games: login, wallet, fairness and the admin console.
Needs Docker; Go 1.27+ only to build outside Docker.

```sh
cp .env.example .env     # dev secrets; for the admin console set ADMIN_ADDR=:8081 and
                         # ADMIN_TOTP_KEY=$(openssl rand -hex 32)
make up                  # Postgres, Centrifugo and the app (add PROFILE=tigerbeetle for TigerBeetle)
```

`make up` copies `gamify.example.yaml` to `gamify.yaml` once, generates Centrifugo's
`infra/centrifugo/config.json` from it and starts everything in Docker Compose:

| What | Where |
|---|---|
| Game API (login `POST /auth/{provider}`, `/healthz`, `/readyz`) | http://localhost:8080 |
| Client WebSocket (Centrifugo) | ws://localhost:8000/connection/websocket |
| Admin console (loopback only) | http://localhost:8081 |

Create the first console user, then log in and enrol TOTP with an authenticator app:

```sh
docker compose exec app gamify admin add-user owner@example.com owner   # prints a one-time password
```

`make down` stops it.

## Build a brand with your own games

A brand is its own Go module: a `main` that wires gamify, plus a package per game.
[docs/writing-a-game.md](docs/writing-a-game.md) is the full guide; this is the path end to end.

**1. Create the module and depend on gamify.**

```sh
go mod init example.com/mybrand
go get github.com/amir1430/gamify@v1.x.y
```

Until a version is published, point at a local checkout instead:

```sh
go mod edit -replace github.com/amir1430/gamify=../gamify -require github.com/amir1430/gamify@v0.0.0
```

**2. Copy the sample `main`** (`cmd/gamify/main.go`) to `cmd/mybrand/main.go` and register your
games after `fairness.Register`:

```go
fair, err := fairness.Register(ctx, a, sealer)   // only if a game draws random outcomes
...
if _, err := coinflip.Register(ctx, a, fair); err != nil {
	return err
}
```

**3. Write a game** in `games/<name>/`: its migrations (embedded, own goose table), sqlc queries
and a `Register(ctx, *app.App, …)` that:

- claims a `ledger.Game` ID with `a.Ledger.RegisterGame` (`FirstBrandGame..MaxGame`, one per
  table of operation IDs, never changed),
- migrates with `a.Migrate(ctx, migrations, "goose_<name>")`,
- declares limits with `a.Games.Game(name, maxAmount, maxPayout, variants)` and checks each
  amount with `Check`,
- registers RPCs (`a.Router.Handle("<name>.<verb>", realtime.JSON(...))`), its reconciler
  (`a.Reconcile`) and its activity history (`a.History`),
- records a row first, then moves money through the ledger with the row ID (`SettleInstant`,
  `Hold`/`SettleHeld`, pots, `Spend`, `Transfer`, `Reward`), so retries and the reconciler
  never move money twice,
- for games of chance, draws with `fair.Next(ctx, tx, userID, n)` in the row's transaction.

Your `sqlc.yaml` lists the gamify schemas your tables reference, as paths on disk (sqlc only
takes relative paths):

```yaml
schema: [../gamify/db/migrations, ../gamify/fairness/migrations, games/coinflip/migrations]
```

**4. Test it through its RPCs** with `apptest`: a real App on a fresh Postgres database per test,
the Postgres ledger and an in-memory transport.

```go
a := apptest.New(t)
fair, _ := fairness.Register(ctx, a.App, apptest.Sealer(t))
coinflip.Register(ctx, a.App, fair)
alice := a.User("alice")
a.Fund(alice, "usd", 1_000)
res, cerr := apptest.Call[coinflip.BetResult](a, alice, "coinflip.bet", coinflip.BetRequest{…})
```

```sh
TEST_DATABASE_URL=postgres://postgres:pw@127.0.0.1:5432/postgres?sslmode=disable go test ./...
```

**5. Configure and run.** Take `gamify.example.yaml` (infra) and `.env.example` (secrets and
product settings) from this repo; see [Configuration](#configuration). Run Postgres and
Centrifugo (this repo's `docker-compose.yml` is a template), generate Centrifugo's config, then
start your binary:

```sh
set -a; . ./.env; set +a                     # load secrets first: gamify.yaml reads them as ${VAR}
go run github.com/amir1430/gamify/cmd/gamify-config -f gamify.yaml -o centrifugo.json
go run ./cmd/mybrand
go run ./cmd/mybrand admin add-user you@example.com owner
```

When the app runs on the host and Centrifugo in Docker, point `centrifugo.api_addr` at the
published port (`127.0.0.1:10000`) and `centrifugo.proxy_endpoint` at
`grpc://host.docker.internal:10001`. Clients connect to Centrifugo from an origin listed in
`http.allowed_origins`.

## Layout

| Package | What |
|---|---|
| `app/` | Wiring: migrations, ledger init, login (`POST /auth/{provider}` → realtime token, `auth.link`), `/healthz` `/readyz`, core RPCs (`wallet.*`, `games.config`), activity history registry, reconcile loop, `Run` |
| `ledger/` | All money logic (instant settles, holds, extra holds, PvP pots and pools, spends, user-to-user transfers, deposits, rewards, swaps, play-only welcome grants), written once on `ledger.Driver` |
| `ledger/tigerbeetle/` | Driver: TigerBeetle 0.16.46 (client version must equal the server's) |
| `ledger/postgres/` | Driver: double-entry Postgres with TigerBeetle's semantics, for brands without TigerBeetle |
| `ledger/ledgertest/` | Conformance suite every ledger driver must pass |
| `realtime/` | `Transport` interface + transport-agnostic RPC `Router` (`Handle`, `JSON`, `ClientError`, error codes) |
| `realtime/centrifugo/` | Driver: Centrifugo v6 (gRPC server API out, gRPC RPC proxy in, HS256 connection tokens) |
| `auth/` | `Provider` interface (credential → `Identity`), users and their linked identities (`user_identities`, one per provider); `Users.Unlink` for admins, never a player RPC |
| `auth/telegram/` | Provider: Mini App initData. Also Login Widget validation, Mini App links, Bot API helpers, `ChatID` |
| `auth/custom/` | Provider: a brand-signed HS256 JWT (`sub`, `aud`, `exp` ≤ 15 min ahead), for brands with their own accounts |
| `fairness/` | Opt-in module for games of chance (`Register`: `seed_pairs` table, `seed.info`, `seed.rotate`): seed pairs sealed with AES-256-GCM, nonces, verifier-compatible hashes and floats, pot seeds |
| `admin/` | Opt-in back office (own migrations, goose table `goose_admin`): console users with roles and permissions, argon2id passwords, TOTP, audit log, balance adjustments, brand views, the console UI (`admin/ui`, embedded); `CreateUser` for the first owner |
| `party/` | Opt-in module (goose table `goose_party`): parties with a leader, join requests, chat, `Lock`/`Unlock` for other modules; `party/partyadmin` is its console view |
| `restrict/` | Opt-in module (goose table `goose_restrict`): per-player restrictions set by operators. Suspended: no login (`App.LoginCheck`, 403) and every RPC refused; blocks: groups of RPC methods the brand names (`Config.Blocks`); each until lifted or a time. Checked in memory (`Router.Check`), reloaded every 5 s; a suspension drops the player's connections. `restrict/restrictadmin` is its console page (`PermRestrict`) |
| `matchmaking/` | Opt-in module (goose table `goose_matchmaking`, needs `party`): queues with parties, ratings (OpenSkill), staked matches (`HoldPot`/`SettlePot`), direct matches (`Create`), `Finish`/`Cancel`, timeouts; `matchmaking/matchmakingadmin` is its console views |
| `gameconfig/` | Admin-set limits per game × currency × variant (code defaults are hard caps) |
| `metrics/` | Prometheus metrics and request logs |
| `db/` | Platform migrations + `Migrate` (each module migrates with its own goose table) |
| `apptest/` | A real `App` for tests: fresh Postgres database per test, Postgres ledger, in-memory transport; `User`, `Fund`, `Call`, `Pushes`, `ReconcileNow` |
| `config/` | `gamify.yaml`: infra settings (Postgres, ledger driver, Centrifugo, addresses), secrets as `${VAR}`, Centrifugo's `config.json` generated from it. No cgo |
| `config/drivers/` | Builds the pool, ledger driver and Centrifugo transport a `config.Config` describes |
| `cmd/gamify/` | Sample brand binary: infra from `gamify.yaml`, product settings from env |
| `cmd/gamify-config/` | Checks a `gamify.yaml` and generates Centrifugo's `config.json` from it |
| `e2e/` | End to end over the real path (Docker), per ledger driver |

### Drivers

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

## Money

The ledger driver is the source of truth for balances; games store metadata only and never check a
balance themselves. Amounts are `int64` minor units, never floats.

| Money shape | Calls |
|---|---|
| Outcome known at once | `SettleInstant` |
| Multi-step | `Hold`, `HoldExtra`, then `SettleHeld` / `SettleHolds` |
| Players against each other | `HoldPot`, then `SettlePot` (at most `MaxPotWinners`), `SettlePool` or `VoidPot` |
| The brand sells something | `Spend` (into the per-currency `sales` account) |
| Player to player | `Transfer` (only in a `Transferable` currency) |
| The brand gives something | `Reward`, welcome grants |
| Outside the games | `Deposit`, `Swap`, `Adjust` (admin) |

Every call is idempotent: transfer IDs come from the game ID and the row ID, so a retry or the
reconciler can't move money twice. A refused ID stays refused (`ErrAlreadyFailed`).

Currencies are configured per brand: `ledger.CurrencyConfig{Code, Ledger, MinAmount, Welcome,
Decimals, Transferable}`. The first is the default. `Welcome` > 0 grants that much once per user
and keeps it play-only: sales, PvP entries and transfers can't take the balance below it.
`Transferable` lets users send the currency to each other; off by default, so real money doesn't
move between players unless the brand opts in. Never change a currency's `Ledger` once money moved.

ID layout, the same on every driver: user account `(ledger, user)`, system account
`(0, ledger<<8 | kind)`, game operation transfer `(game<<8 | leg, row)`, spends and transfers
`(ns<<32 | game<<8 | leg, row)`, others `(ns<<32 …, row)`. The casino's TigerBeetle data uses fixed
system account IDs and other namespaces, so moving the casino onto this platform needs a balance
migration (or a compat layout); it can't reuse its cluster as-is.

## Configuration

Two places, by design: **`gamify.yaml`** for infra, **env** for secrets and product settings.
Secrets never go in the YAML; it references them as `${VAR}` (`$$` is a literal `$`). The binary
reads `gamify.yaml` from the working directory, or the file `GAMIFY_CONFIG` names.

`gamify.yaml` (see `gamify.example.yaml`):

| Key | What |
|---|---|
| `postgres.dsn`, `postgres.max_conns` | the database; `${DATABASE_URL}` |
| `ledger.driver` | `postgres` or `tigerbeetle` (`ledger.tigerbeetle.cluster_id`, `addresses`) |
| `centrifugo.api_addr`, `api_key`, `token_secret`, `token_ttl` | the gRPC API the app dials; secrets as `${VAR}` |
| `centrifugo.proxy_listen`, `proxy_endpoint`, `proxy_timeout` | where the app serves Centrifugo's RPC proxy, and how Centrifugo reaches it |
| `centrifugo.namespaces` | the brand's channel namespaces (`name`, `presence`, `subscribe`: clients may subscribe), added to the platform's `user`, `online`, `feed` |
| `http.addr`, `http.allowed_origins` | the game API and the Mini App's origins (`"*"` is refused) |
| `metrics.addr` | Prometheus; keep it private, `off` disables |
| `log.level` | `debug`, `info`, `warn`, `error` |

`go run ./cmd/gamify-config -f gamify.yaml -check` validates it (`make check`); `-o config.json`
writes Centrifugo's config. That config sends every RPC to the proxy (`without_namespace`) and never
holds a secret: Centrifugo reads `CENTRIFUGO_GRPC_API_KEY` and
`CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY` from its env.

Env read by the sample `main` (see `.env.example`):

| Variable | What |
|---|---|
| `DATABASE_URL`, `CENTRIFUGO_API_KEY`, `CENTRIFUGO_TOKEN_SECRET` | secrets `gamify.yaml` references |
| `TG_BOT_TOKEN` | Telegram login |
| `AUTH_JWT_SECRET` (≥ 32 bytes), `AUTH_JWT_AUDIENCE` | brand-signed JWT login; at least one login provider |
| `SEED_KEY` (`openssl rand -hex 32`), `SEED_KEY_ID` | seals fairness server seeds; bump the ID with a new key to rotate |
| `CURRENCIES` | `code:ledger:minamount[:welcome[:decimals[:transferable]]]`, comma-separated, e.g. `usd:1:1:0:2,coins:2:1:1000:0:t` (`decimals` display only, ≤ 18; `transferable` is `t` or omitted) |
| `DEV_FAUCET` | `true` adds `wallet.faucet` (free test money). Never with real money |
| `ADMIN_*` | the console, below |

## Admin console

Off by default. Set `ADMIN_ADDR=:8081`, `ADMIN_ORIGIN` (the URL you browse to, e.g.
`http://localhost:8081`) and `ADMIN_TOTP_KEY` (`openssl rand -hex 32`); optional `ADMIN_ALLOW_IPS`
(CIDRs) and `ADMIN_APPROVE_ABOVE` (`usd:10000,eur:5000`, minor units: larger adjustments need a
second admin). `TG_BOT_TOKEN` also enables Telegram login to the console. Compose publishes it on
`127.0.0.1:8081` only.

```sh
gamify admin add-user <email> <owner|operator|support|viewer>   # prints a one-time password
```

Pages: dashboard (players online, system balances per currency), players (profile, linked logins,
wallet and adjustments, activity per game, seed pairs), games (on/off and limits per currency),
adjustments with approval, audit log, console users, account. Games can add their own pages
(`admin.View`, see the guide).

The console's UI is `admin/ui` (Vite, React, Refine, shadcn), built into `admin/ui/dist`, which is
committed and embedded in the Go binary: Node is needed only to edit the UI. `make admin-ui`
rebuilds it (`npm ci` + build); `make admin-ui-check` fails if the committed build is stale.
While editing, run the Go server with `ADMIN_ORIGIN=http://localhost:5173`, then
`ADMIN_ADDR=:8081 npm --prefix admin/ui run dev` serves the UI with hot reload on
`http://localhost:5173` and proxies `/api` to `ADMIN_ADDR`. `npm --prefix admin/ui test` runs its
unit tests (`node --test`), `npm --prefix admin/ui run check` type-checks.

## Development

macOS: export `CGO_LDFLAGS=-Wl,-ld_classic` before building (TigerBeetle's static lib vs Apple's
new linker).

```sh
sqlc generate                    # after editing queries or migrations; never edit internal/store
go test ./...                    # unit tests; integration tests skip without the env below
```

Integration tests need a Postgres server they can create databases on and, for the TigerBeetle
driver, a TigerBeetle 0.16.46 replica:

```sh
docker run -d --name gamify_test_pg -e POSTGRES_PASSWORD=test -p 127.0.0.1:55432:5432 postgres:17-alpine
export TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable'
export TEST_TB_ADDRESS=127.0.0.1:3000   # optional: a running TigerBeetle replica
go test ./...
```

A ledger change must pass `ledgertest` on Postgres *and* TigerBeetle.

```sh
e2e/run.sh                       # fresh Postgres + TigerBeetle + Centrifugo in Docker; plays over the real path, per ledger driver
```

Project rules (money, IDs, fairness, migrations, sqlc) are in [CLAUDE.md](CLAUDE.md).

Regenerate the Centrifugo protos (only when bumping Centrifugo):

```sh
protoc -I proto/centrifugo --go_out=realtime/centrifugo/apiproto --go_opt=paths=source_relative --go-grpc_out=realtime/centrifugo/apiproto --go-grpc_opt=paths=source_relative api.proto
protoc -I proto/centrifugo --go_out=realtime/centrifugo/proxyproto --go_opt=paths=source_relative --go-grpc_out=realtime/centrifugo/proxyproto --go-grpc_opt=paths=source_relative proxy.proto
```

## Versioning

Semver. Brands depend on tagged versions (`go get github.com/amir1430/gamify@v1.x.y`); migrations
run on start, so an upgrade is a `go get` and a deploy. Within a major version exported APIs don't
break and committed migrations never change (new ones are added). Generated query code is in
`internal/store` packages and isn't public API. [CHANGELOG.md](CHANGELOG.md) lists every release
with upgrade notes.

## Not in the platform yet

Planned modules, each when a brand needs it: leaderboards, referrals, a payments provider
(deposits and withdrawals, see `gamify platform changes.md`), and an exchange service with rates
over `ledger.Swap`. Until then a brand builds them on the ledger, realtime and admin views.
