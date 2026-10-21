# gamify: platform changes

Oct 21, 2026 · @Amir

## Summary

Four changes turn gamify from a Telegram casino core into a generic platform: auth providers, opt-in fairness, a payments driver, and three small cleanups. Ledger and realtime (Centrifugo) stay as they are. Section 5 says how brands build on it after v1.

| # | Change | Why | Size |
| --- | --- | --- | --- |
| 1 | Auth becomes a `Provider` driver | Today a brand without Telegram can't start: `app.New` requires `BotToken`, `users.telegram_id` is `NOT NULL` | \~150 lines + migration |
| 2 | Fairness becomes an opt-in module | Only casino games need seeds; social, skill and puzzle games don't | \~80 lines moved |
| 3 | Payments becomes a `Provider` driver | Deposits are still casino-only; every brand pays in a different way (Stars, TON, Stripe) | New package |
| 4 | Cleanups | Bot API sits in `auth`, gin leaks into brands, metrics panic on a second `App` | Small, independent |
| 5 | Brand integration | Brands with their own developers import gamify as a Go module; it needs a stable API and tooling | API cleanup before v1, then packages |

## 1. Auth providers

Login goes through an `auth.Provider` the brand picks; Telegram becomes the first driver in `auth/telegram`. A user is an internal ID with one or more linked identities, as in Nakama.

**Where Telegram is hard-wired today**

- `app/app.go:65`: `app.New` fails without `cfg.BotToken`.
- `app/app.go` `routes()`: `/auth/telegram` calls `auth.ValidateInitData` directly.
- `db/migrations/00001_users.sql`: `telegram_id bigint NOT NULL UNIQUE`.
- `db/queries/auth.sql`: `UpsertUser` conflicts on `telegram_id`; `SearchUsers` and `GetUsers` select it; `GetTelegramID`.
- `auth/users.go`: `Users.Upsert(tg TelegramUser)`, `User.TelegramID`.
- `games/dice/integration_test.go:67` and `e2e/smoke.mjs:47` insert or log in by Telegram ID.

**Interface**

```go
// auth
type Identity struct {
	Provider, Subject           string // "telegram"/"12345", "device"/uuid, "custom"/brand id
	Username, DisplayName, Lang string
	Meta                        map[string]string // telegram start_param, etc.
}

type Provider interface {
	Name() string
	Verify(ctx context.Context, credential json.RawMessage, now time.Time) (Identity, error)
}
```

**Schema**

```sql
CREATE TABLE user_identities (
    provider text   NOT NULL,
    subject  text   NOT NULL,
    user_id  bigint NOT NULL REFERENCES users(id),
    PRIMARY KEY (provider, subject)
);
-- users: drop telegram_id; keep username, first_name (rename display_name), language_code
```

Nothing is committed yet, so if no brand is deployed, edit `00001_users.sql` in place. Otherwise add `00003` that copies `telegram_id` into `user_identities` and then drops the column.

**Wiring**

- `app.Drivers.Auth []auth.Provider` replaces `Config.BotToken`; `app.New` requires at least one.
- One route: `POST /auth/{provider}` with the provider's credential as the body. Same flow as today: verify, upsert user + identity, `Ledger.EnsureUser`, return `Realtime.Token`.
- `Users.Upsert(ctx, Identity)`: one transaction, find the identity or create user + identity. Two concurrent first logins with the same identity race on the `user_identities` primary key: insert with `ON CONFLICT DO NOTHING` and read it back, so both get the same user.
- `auth.link` RPC: attaches another identity to the connection's user (never a user ID from the payload). Refuses an identity already linked to someone else, and a second identity at a provider the user already has.
- `Users.Unlink(ctx, userID, provider)`: admin/support only, never a player RPC (whoever holds a session could remove the owner's own login). Never removes a user's last identity.
- Second driver to prove the interface: `custom` (a brand-signed HS256 JWT), so a brand with its own accounts can log in without Telegram.
- No `device` provider for real money: a device UUID isn't a secret, so anyone who knows it can take over the account. If we add it, it's only allowed on brands with virtual currencies.
- `GetTelegramID` becomes an identity lookup (`provider = 'telegram'`) in `auth/telegram`. The Bot API helpers use it for the chat ID (`SendMessage`, `PrepareShare`).

**Sessions (open question)**

Today a session is the Centrifugo token (1h); the client renews by sending initData again, valid 24h. Providers whose credential can't be replayed (email/password, OAuth codes) need a refresh token. Add it with the first such provider, not now.

**Tests**

- Move `TestValidateInitData` / `TestValidateLogin` to `auth/telegram`.
- One integration test: same identity twice gives one user; `auth.link` of a taken identity is refused.

## 2. Fairness as an opt-in module

Keep `fairness`, but move it out of core: a generic platform needs it only for games of chance (dice, mines, crash). A brand that runs only social, skill or puzzle games should run without a seed key or a `seed_pairs` table. It plugs in like a game does.

**Where it is core today**

- `app.Config.Sealer` is required by `app.New`; `SEED_KEY` is required by `cmd/gamify`.
- `app.New` always builds `App.Fair`; login calls `a.Fair.Ensure`.
- `seed.info` and `seed.rotate` are core RPCs in `app.methods()`; `App.RotationGuard` lives in `app`.
- `seed_pairs` is in the platform migration `db/migrations/00001_users.sql`.

**Change**

```go
// fairness
// Register adds seeds to an app: its table, seed.info, seed.rotate.
func Register(ctx context.Context, a *app.App, sealer *Sealer) (*Service, error)

func (s *Service) Guard(g Guard) // replaces App.RotationGuard

// a brand's main
fair, err := fairness.Register(ctx, a, sealer)
_, err = dice.Register(ctx, a, fair)
```

- `seed_pairs` moves to `fairness/migrations/00001_seed_pairs.sql` (goose table `goose_fairness`); queries move to `fairness/queries.sql`; update `sqlc.yaml`.
- Drop `Fair.Ensure` from login: `Service.Next` already creates the pairs on the first bet. `seed.info` must call `Ensure` too, so a player can see the server seed hash before the first bet. A first bet on a seed whose hash the player never saw isn't provably fair.
- `fairness.Service` stays free of `app`; only `Register` imports it, in its own file. The package with the pinned vectors doesn't depend on wiring.
- PvP games need the module too: pot seeds (`NewPotSeed`, `OpenPotSeed`) use the Sealer.
- `app` loses `Fair`, `Sealer`, `RotationGuard` and the `seed.*` RPCs.
- The fairness rules in `CLAUDE.md` stay; they now apply to the module, not core.

The ledger stays core: games without real money still use it for virtual currencies, like Nakama's wallet.

## 3. Payments provider

A new `payments` module turns provider events into ledger deposits; each provider is a driver in `payments/<name>`. The ledger already does the money side, so a provider only verifies and reports.

```go
// payments
type Provider interface {
	Name() string
	// Invoice starts a deposit; key is the client's idempotency key.
	Invoice(ctx context.Context, userID int64, c ledger.Currency, amount int64, key string) (Invoice, error)
	// Webhook verifies a provider callback and returns the paid deposit, if any.
	Webhook(r *http.Request) (Deposit, bool, error)
}
```

- Module owns a `payments` table: provider, external ID (unique per provider), user, currency, amount, status.
- Credit with the existing `ledger.Deposit(ctx, c, depositID, userID, amount)` (`nsDeposit`); no new namespace. `depositID` is the `payments` row ID: insert with `ON CONFLICT (provider, external_id) DO NOTHING` and read it back, so a webhook retried twice gets the same row and credits once.
- `Webhook` checks the paid amount and currency against the stored invoice. A callback that doesn't match credits nothing; we never credit the amount the provider reports.
- Route `POST /payments/{provider}/webhook`; RPC `payments.invoice` carries an idempotency key.
- Withdrawals: add to the interface with the first provider that supports them.
- Refunds and chargebacks (Stripe disputes, Stars refunds) take back money the user may have spent, and drivers refuse overdrafts. Decide before the first real provider: debit what's left, record the rest, flag the user.

**Open question:** which provider first? Telegram Stars fits the Mini App brands; it needs no extra account. It doesn't fit `Webhook(*http.Request)` as is: payments come as bot updates, and `pre_checkout_query` must be answered within 10 seconds. Stars are paid in XTR, so a brand currency needs a rate.

## 4. Cleanups

Three small changes, each independent of the others.

| Change | Where | Why |
| --- | --- | --- |
| Move Bot API helpers to `auth/telegram` | `auth/bot.go` | Sending messages isn't auth; it goes with the Telegram driver |
| Replace gin with `net/http` | `app/app.go`, `metrics.Gin` | `App.HTTP *gin.Engine` forces gin on every brand; \~4 routes, Go's `ServeMux` handles `POST /auth/{provider}` |
| Register pool metrics per `App` | `metrics.Pool` in `App.Run` | Global `promauto` registration panics on a second `App` in one process (tests). A pool collector per `App`, not a `sync.Once`: a once would leave the second pool without metrics |

## 5. How brands integrate after v1

Brands with their own developers import gamify as a Go module (`go get github.com/amir1430/gamify@v1.x`) and write their own `main` and games. No forks: a fork misses ledger and fairness fixes, and a changed `ledger` or `fairness` no longer has the guarantees of `ledgertest` and the public verifier. No hosted multi-tenant setup: one deployment runs one brand.

```go
// a brand's main
a, err := app.New(ctx, cfg, app.Drivers{Pool: pool, Ledger: tb, Realtime: cf,
	Auth: []auth.Provider{telegram.New(botToken), custom.New(secret)}})
fair, err := fairness.Register(ctx, a, sealer) // only games of chance
_, err = dice.Register(ctx, a, fair)           // our games
_, err = mygame.Register(ctx, a)               // the brand's own game, in its repo
err = a.Run(ctx)
```

Client: `POST /auth/{provider}` returns a Centrifugo token; connect, then call the RPCs (`wallet.*`, `games.config`, `<game>.*`, `payments.invoice`). Each money-moving call carries an idempotency key.

**Wallet model.** Transfer wallet only: money lives in our ledger and comes in and goes out through `payments`. A seamless wallet (our games debit and credit an operator's wallet through its API) is out of scope until a brand needs it. It isn't a `ledger.Driver`: a remote wallet can't provide linked chains or pending/post/void.

**What v1 needs**

| # | Change | Why | When |
| --- | --- | --- | --- |
| 1 | Generated `store/` packages under `internal/`; public API is `app`, `auth`, `payments`, `ledger`, `realtime`, `fairness`, `gameconfig` | A brand importing `auth/store` breaks on any query change | Before v1 |
| 1 | Semver; after the v1 tag no committed migration changes, breaking changes go in `v2` | Upgrades are `go get`; migrations run on start | Before v1 |
| 2 | `ledger.Game` range for brands' games (≥ 1<<20); ours stay below | IDs must be unique in a deployment and never change | Before v1 |
| 2 | "Write your own game" guide: deterministic bet IDs, nonce in the bet's transaction, no `math/rand`, reconciler for held bets | The `CLAUDE.md` rules, for brand developers | Before v1 |
| 3 | `apptest`: an `App` on the Postgres ledger with an in-memory transport, `Call(user, method, payload)` | No fake `realtime.Transport` exists; testing one RPC needs Centrifugo | With v1 |
| 4 | TypeScript client SDK: login, Centrifugo connection, typed RPCs, idempotency keys added for you | Every brand writes this otherwise, and idempotency is the part they get wrong | With v1 |
| 5 | Leaderboards, rewards, referrals, admin API as opt-in modules (`leaderboard.Register(ctx, a)`) | Otherwise each brand builds its own and they drift | One at a time, when a brand asks |
| 6 | Changelog with upgrade notes per release (migrations, API changes) | Brands upgrade without reading our diffs | Every release |

## Order

1. Cleanups (gin, metrics): small, and tests need them. **Done.**
2. Auth providers with the `custom` driver. **Done.**
3. Fairness opt-in. **Done.**
4. Section 5 items 1 and 2, then tag v1. **Done** except the tag (needs a first commit).
5. `apptest` and the client SDK. `apptest` **done**; SDK next.
6. Payments, once the first provider is chosen.

## Not changing

These stay concrete; each gets a driver only when a brand needs a second implementation.

| Part | Decision |
| --- | --- |
| Realtime | Centrifugo only for now. Add a second driver (in-process WebSocket) when a brand needs it. |
| Database | Postgres only. sqlc, goose and `pgx.Tx` depend on it; CockroachDB works through the Postgres protocol. |
| gameconfig storage | A small table polled every 5s; no second backend needed. |
| Metrics | Prometheus. Add OpenTelemetry if a brand asks. |
| Seed `Sealer` | Concrete with key IDs. An interface when a brand needs KMS or Vault. |
