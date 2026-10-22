# Changelog

All notable changes, for brands upgrading. Format: [Keep a Changelog](https://keepachangelog.com),
versions: [semver](https://semver.org). Upgrade notes call out migrations and API changes.

## Unreleased

### Added
- `ledger`: `FeeOf(pot, bp)`, `SettlePotFee` and `SettlePoolFee` (the house's cut set per pot, 0..10_000
  basis points; `SettlePot`/`SettlePool` are the `FeeBP` case, transfer IDs unchanged), and
  `RewardPrize(ctx, game, currency, claimID, userID, amount)`, a prize from the rewards account in
  its own transfer-ID namespace.
- `app`: `App.Loop(every, f)`, a periodic job `Run` ticks until it ends (like the reconcile ticker).
- `apptest`: `Transport.SetOnline(ids...)` sets who `Online` reports (default nobody).
- `party`: new opt-in module: parties with a leader, closed-party join requests, chat/data messages
  and `Lock`/`Unlock` for other modules. `party.Register(ctx, app, party.Config{})` adds the
  `party.*` RPCs and the `Service` (`Get`, `Of`, `OnChange`). Config: `PartiesPerUser`, `MaxSize`,
  `MessageBytes`, `MessagesPerSec`. Client errors 1020-1026.
- `matchmaking`: new opt-in module, started: its tables, `Register(ctx, app, parties, Config{})`,
  `Service.Queue(QueueConfig)` (validated queue with defaults; `Queue.Config()`), the `Team`,
  `Ticket`, `Match`, `Player`, `Result` types, `Forever`, client errors 1030-1036, and the OpenSkill
  `Rating`/`DefaultRating`/`Ordinal`. The ledger game ID `matchmaking.Game = 1` is claimed by `Register`.
  A queue needs 2+ teams unless FFA, a non-empty stake list per currency, `NumberRule.Every > 0`,
  and non-negative `FillAfter`/`MatchTimeout`/`MaxWait` (`Forever` allowed). The matcher (internal)
  fills teams anchor first, then larger parties first, oldest first among equal sizes.

### Upgrade
- `party`: new goose table `goose_party` (migrations run in `party.Register`); nothing changes unless you register it.
- `matchmaking`: new goose table `goose_matchmaking` (migrations run in `matchmaking.Register`, which also
  registers platform ledger game 1); nothing changes unless you register it.

## [1.0.0] - 2026-10-22

First public version, extracted from the casino's core and made game-neutral: the platform ships
no game and holds no game logic; every game, casino or not, brings its own rules.

### Added
- `admin`: new opt-in module, started: its tables (`admin_users`, `admin_sessions`,
  `admin_audit`, `admin_adjustments`), roles and permissions (`Role`, `Perm`, `Role.Can`,
  `Role.Valid`), `Config` and `Brand` types, argon2id passwords, TOTP (RFC 6238), and
  `CreateUser(ctx, pool, email, role)` (returns a one-time random password).
- `admin`: `Register(ctx, app, Config)` serves the console on its own listener (`Config.Addr`,
  through `App.Serve`; `Config.Origin` and a 32-byte `Config.TOTPKey` required): password + TOTP
  login (enrolment on first login), optional Telegram Login Widget login, lockout (at most 5
  failed logins per email or Telegram ID in any 15 minutes, then 429 `locked`) and at most 4
  login attempts in flight per process (then 429 `busy`, `Retry-After: 1`), cookie sessions (idle 30 min, absolute 12 h), CSRF (`X-Admin` header
  and `Origin`), `AllowIPs`, an audit row for every login, enrolment, logout and `CreateUser`;
  routes `/api/login`, `/api/login/telegram`, `/api/totp/enrol`, `/api/logout`, `/api/me`,
  `/api/brand/logo`. `Admin.Handler` for tests.
- `admin`: read endpoints for `view` roles: `GET /api/dashboard`, `/api/users` (+ `/{id}`,
  `/{id}/activity`, `/{id}/seeds`), `/api/games`, `/api/audit`, `/api/adjustments`. Lists page with
  `?limit=` (default 50, max 100) and `?before=` and answer `{"items", "next"}`.
- `admin`: write endpoints, all audited in the change's transaction: `PUT /api/games/{game}`,
  `PUT`/`DELETE /api/games/{game}/limits/{currency}/{variant}` (`_` = whole game; reloads the
  config and publishes it on `feed:config`), `DELETE /api/users/{id}/identities/{provider}` and
  `POST /api/users/{id}/disconnect` (both need a `reason`), console users (`GET`/`POST
  /api/admins`, `PATCH /api/admins/{id}`: role, disable, reset TOTP, reset password; kills their
  sessions; the last enabled owner stays) and `POST /api/me/password`. A write that changes
  nothing writes no audit row.
- `admin`: balance adjustments, the console's only way to move money: `POST /api/adjustments`
  (`adjust`; `{user_id, currency, amount, reason, idempotency_key}`, amount in minor units, < 0
  debits; a replay of the key returns the stored row, with different fields 409
  `idempotency_conflict`), and `POST /api/adjustments/{id}/approve` / `reject` (`approve`; not
  the requester's own, 403 `own_adjustment`; reject needs a `reason`). Above
  `Config.ApproveAbove[currency]` an adjustment waits for approval, otherwise it is applied with
  `Ledger.Adjust` straight away and marked `done`, or `failed` when the ledger refuses a debit
  beyond the balance. A reconciler (`App.Reconcile`) re-applies `approved` rows older than 10 s.
  Audited as `adjust.request`, `adjust.approve`, `adjust.reject`. `Register` refuses a negative
  `ApproveAbove` threshold.
- `admin`: brand views. `Admin.View(admin.View{…})` describes a page in Go (columns, filters,
  keyset `List`, optional `Get`, actions with typed inputs; types `Text`, `Int`, `Decimal`,
  `Money`, `Bool`, `Time`, `Enum`, `UserID`, `JSON`) and the console draws it; call it after
  `Register` and before `App.Run`. Routes `GET /api/views` (`{"items": [...]}`), `GET
  /api/views/{name}` (filters, `?before=`, `?limit=`), `GET /api/views/{name}/{id}` and `POST
  /api/views/{name}/{id}/actions/{action}` (`{reason, input}`; runs in a transaction with an
  audit row `view.<view>.<action>`, target `<view>:<id>`; an error or panic rolls back, 500
  `action_failed`). Integers beyond ±2^53 in integer columns are sent as JSON strings.
- `admin`: `Config.Perms map[Perm][]Role`, brand permissions for views and actions; owners hold
  every permission and `/api/me` lists the ones the role holds. `Register` refuses an empty key,
  a built-in name or an unknown role. `Role.Can` still answers built-ins only.
- `admin`: the console UI (React, Refine, shadcn; built into `admin/ui/dist` and embedded, so
  brands need no Node): sign-in with TOTP and first-login enrolment (QR), the shell (grouped
  sidebar, account menu), light, dark and system themes, `Brand.Accent` as its accent. Every
  non-`/api` path serves the app (client-side routes); unknown `/api` paths answer a JSON 404
  `not_found`. `Config.UI` replaces it whole and gets the same routing. Pages: dashboard
  (players online, system balances per currency), players and the player page (profile, linked
  identities with unlink, wallet and adjustments, activity per game, seed pairs, disconnect), games
  (on/off, per-currency limits with caps and overrides, edit/clear), adjustments (request with a
  confirmation step, approve/reject; the sidebar counts those waiting), audit log (filters,
  before/after view), console users (create, role, disable, reset TOTP or password), account
  (password change). Amounts are typed as decimal text and parsed exactly; anything else
  (`1.005` with 2 decimals, `1e3`, `-0`, spaces, grouping) is refused, never rounded.
- `admin`: `/api/me` currencies carry `approve_above` (`Config.ApproveAbove`, `null` = never
  waits), so the console says before sending whether an adjustment needs approval.
- `admin`: `ErrEmailTaken`, returned (wrapped) by `CreateUser` for an email in use; it was a
  plain error before, so match it with `errors.Is`.
- `ledger`: `Ledger.Decimals(c)` (a currency's display decimals).
- `fairness`: `Service.PairsOf` (a user's seed pairs for the admin; seeds only once revealed).
- `auth`: `Users.UnlinkTx` (`Unlink` inside the caller's transaction) and `Users.Identities`
  (a user's linked logins with link time).
- `gameconfig`: transactional writes for the admin (`SetGame`, `SetLimit`, `DeleteLimit` take a
  `pgx.Tx` and refuse overrides above the code caps) and `Overrides` (cached view). Writes don't
  reload the cache: call `Load` after commit.
- `app`: `App.Serve` for extra listeners; `App.History` registry of games' activity history
  (`app.Activity`, `app.HistorySource`, `App.HistorySources`); a game without money leaves amount
  and payout 0 and puts its own fields in `Detail`.
- `app`: wiring (migrations, ledger init, login, health, core RPCs `wallet.*`, `games.config`,
  `auth.link`, reconcile loop) on `net/http` (`App.HTTP` is a `*http.ServeMux`).
- `auth`: login through `auth.Provider` drivers; users with linked identities (one per
  provider); `Users.Unlink` for admins. Drivers: `auth/telegram` (Mini App initData),
  `auth/custom` (brand-signed HS256 JWT with `aud` and a lifetime cap).
- `fairness`: opt-in module (`fairness.Register`) with `seed.info` / `seed.rotate`.
- `ledger`: TigerBeetle and Postgres drivers with one conformance suite; game IDs checked
  (`1..MaxGame`) and claimed with `RegisterGame`; brands' games from `FirstBrandGame`.
- `config` + `config/drivers`: infra from `gamify.yaml` (secrets as `${VAR}`), Centrifugo's
  `config.json` generated by `cmd/gamify-config`.
- `docs/writing-a-game.md` for brands' own games: casino rounds, matches with pots, shops,
  gifts and endless runners. `cmd/gamify` is a sample brand without games.
- `apptest`: a real App for brands' game tests (fresh database per test, in-memory transport,
  `User`, `Fund`, `Call`, `Pushes`, `ReconcileNow`); `App.RunReconcile`.
- Local stack: `docker-compose.yml`, `make up`.
- `ledger`: `Ledger.Adjust` credits or debits a user from/to the currency's new adjust account
  (transfer ID from the adjustment row, so a retry moves money once; a debit can't overdraw);
  `Ledger.SystemBalances` reads every system account of a currency; `CurrencyConfig.Decimals`
  (display only, 0 = whole units); `ErrAlreadyFailed`. `cmd/gamify` reads `CURRENCIES` as
  `code:ledger:minamount[:welcome[:decimals[:transferable]]]` (decimals ≤ 18, transferable `t`
  or omitted).
- `cmd/gamify` (the sample): opt-in admin console via env `ADMIN_ADDR`, `ADMIN_ORIGIN`, `ADMIN_TOTP_KEY` (64 hex chars), `ADMIN_ALLOW_IPS`, `ADMIN_APPROVE_ABOVE` (`usd:10000,…`; `TG_BOT_TOKEN` also enables Telegram console login), and `gamify admin add-user <email> <role>` to create console users. Compose publishes the console on `127.0.0.1:8081` only.

- `ledger`: money calls in neutral terms: `SettleInstant`, `Hold`, `HoldExtra`, `SettleHeld`,
  `SettleHolds` take an operation ID and an amount; `CurrencyConfig.MinAmount` and
  `Ledger.MinAmount` (smallest real-money amount a game may charge). A `ledger.Game` is an ID space
  for one table of operation IDs.
- `ledger`: `Ledger.Spend` (user pays the brand: a shop item, an extra life) into a new `sales`
  system account per currency; the welcome grant may be spent.
- `ledger`: `Ledger.Transfer` (user to user: gifts, trades), only in a currency with
  `CurrencyConfig.Transferable` (else `ErrNotTransferable`); the sender's welcome grant stays
  play-only.
- `ledger`: `MaxPotWinners` (247): `SettlePot` refuses more winners with `ErrTooManyWinners`
  before moving anything (more would overflow a transfer ID's leg).
- `gameconfig`: limits in neutral terms: `Limits{Enabled, MinAmount, MaxAmount, MaxPayout}`
  (JSON `min_amount`, `max_amount`, also in `games.config` and the admin API), `Game.MaxAmount`,
  `Game.Check`; `ErrInvalid` reads "invalid amount". A game that never charges money only uses
  the on/off switch.

### Changed
- `ledger`: a retried failed transfer now returns `ledger.ErrAlreadyFailed` (was an untyped error).

### Upgrade notes
- `admin` has its own migrations (`admin.Migrations`, goose table `goose_admin`). They run only
  when a brand uses the module (`admin.CreateUser` now, `admin.Register` later); brands that
  don't import it get no new tables.
- `ledger.Init` creates two new system accounts per currency (adjust, kind 9; sales, kind 10) on
  start; nothing to run by hand.
