# Admin module

Oct 21, 2026

## Why

Every brand needs an admin: support finds a player and their bets, an operator turns a game off or
tightens its limits, someone fixes a balance after an incident. The casino has one (`internal/admin`,
still casino-only per the README), and `gameconfig` and `auth.Users.Unlink` already expect an admin
to call them. If gamify ships no admin, each brand builds its own against our tables, and those
builds drift and break the rules (direct balance edits, unaudited changes).

We looked at reusing Nakama's console. It's an Angular frontend for Nakama's own Console API and
data model (JSON wallet you can edit, storage, groups). Most screens would have to be rewritten and
the fork maintained, so we take its feature list (console users with roles, audit, player
search) and build our own. Frontend: Refine.

## Goals

- An opt-in module, `admin.Register(ctx, a, cfg)`, like `fairness`: its own tables, migrations
  (goose table `goose_admin`), sqlc queries and HTTP API.
- Console users with fixed roles, MFA, and an audit log of every write and every login.
- v1 screens: dashboard, players (search, detail, identities, balances, bets), game limits,
  balance adjustments, console users, audit log.
- A white-label UI (Refine + shadcn) embedded in the Go module, so a brand gets it with
  `go get`. No Node needed to build a brand's binary.
- Brand games show up without admin code of their own: limits come from `gameconfig.Games()`,
  bet history from a source the game registers on `app` (below).
- Brands add their own views in Go (`adm.View(…)`): tables, detail pages and actions over their own
  tables, drawn by the built-in UI, with the same permissions, audit and CSRF guard as ours. No
  Node for the brand.

## Non-goals

- Player restrictions (bans, self-exclusion), feed, leaderboards, rewards, referrals, swaps and
  payments screens. Each gets its admin endpoints when its module moves over.
- Brand-defined roles. Four fixed roles in v1; brands can define permissions and assign them to
  those roles (Brand views).
- JavaScript plugins in the UI and links out to brand-hosted pages. Plugins come when a brand needs
  something a Go-declared view can't draw. Escape hatch until then: `Config.UI` replaces the bundle.
- Charts and free-form layouts in brand views.
- Reports and charts beyond the dashboard numbers. Prometheus/Grafana covers ops.
- SSO (OIDC, SAML). Add it as a login method when a brand asks.
- Hosting the admin for several brands. One deployment, one brand.

## Anti-goals

These would make it a failure even if it works:

- Any balance change that isn't a ledger transfer with a deterministic ID. No "set balance".
- Showing or returning an unrevealed server seed, anywhere (API, UI, audit `before`/`after`).
- A change without its audit row, or an audit row for a change that rolled back.
- The admin reachable on the players' port by default.
- A brand needing Node, pnpm or a UI build step to compile their `main`.

## Decided

- Refine (MIT) + shadcn/ui, built with Vite, output embedded with `go:embed`. (The user's
  choice.)
- The UI's layout and patterns follow Nakama's console (UI > "Look and layout"). (The user's
  choice.)
- UI code is built without test-first TDD; checked by building and running it. (The user's
  choice.)
- Brand views declared in Go, in v1. (The user's choice, from: Go-declared views, JS plugins,
  links out.)
- Everything else in this document is my call; the user skipped the questions and asked for a
  draft.

## Wiring

```go
// a brand's main
a, err := app.New(ctx, cfg, drivers)
fair, err := fairness.Register(ctx, a, sealer)
_, err = dice.Register(ctx, a, fair)
adm, err := admin.Register(ctx, a, admin.Config{
	Addr:     ":8081",                            // its own listener, never App.HTTP
	Origin:   "https://admin.brand.example",       // the one origin allowed to call the API
	Brand:    admin.Brand{Name: "Brand", Logo: logoPNG, Accent: "#7c3aed"},
	Fairness: fair,                                 // optional: shows seed pairs (revealed only)
	ApproveAbove: map[ledger.Currency]int64{"usd": 100_00}, // adjustments above need a 2nd admin
	Perms:        map[admin.Perm][]admin.Role{"crash.void": {admin.RoleOperator}}, // brand permissions
})
err = diceadmin.Register(adm, diceSvc) // brand views (below)
err = a.Run(ctx)
```

- `Register` migrates, builds the service and adds its server to `App.Run` (a new
  `App.Serve(name string, srv *http.Server)` hook so `Run` starts and shuts it down with the
  others). It must be called after the games, so `gameconfig.Games()` is complete; it reads the
  bet sources lazily anyway.
- Separate listener so a brand can keep it on a private network. `Addr` is required; there is no
  default that exposes it. Optional `AllowIPs []netip.Prefix`.
- The sample `cmd/gamify` turns it on when `ADMIN_ADDR` is set and adds a subcommand
  `gamify admin add-user <email> <role>` (prints a one-time password and the TOTP setup URI). That's
  how the first owner exists. The Go API behind it is `admin.CreateUser(ctx, pool, email, role)`,
  so brands with their own `main` do the same.

## Console users and roles

```sql
CREATE TABLE admin_users (
    id            bigserial PRIMARY KEY,
    email         text        NOT NULL UNIQUE,  -- lowercased
    role          text        NOT NULL CHECK (role IN ('owner', 'operator', 'support', 'viewer')),
    password_hash text        NOT NULL,         -- argon2id, PHC string
    totp_secret   bytea,                        -- sealed like seeds; NULL until enrolled
    telegram_id   bigint      UNIQUE,           -- optional Login Widget login
    disabled_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
```

| Permission | owner | operator | support | viewer |
|---|---|---|---|---|
| View dashboard, players, bets, limits, audit | ✓ | ✓ | ✓ | ✓ |
| Unlink an identity, kick a session | ✓ | ✓ | ✓ | |
| Change game limits, turn games off | ✓ | ✓ | | |
| Request a balance adjustment | ✓ | ✓ | | |
| Approve an adjustment (not their own) | ✓ | | | |
| Manage console users, view their sessions | ✓ | | | |

Permissions are constants in Go (`admin.PermAdjust`, …) and the table above is a map in code.
Every handler declares the one it needs; a test checks every route has one. The UI gets the user's
permissions from `GET /me` and hides what they can't do. The server enforces it either way.

## Login and sessions

- Password + TOTP (RFC 6238). TOTP is required for every role; first login forces enrolment.
  Optional Telegram Login Widget (`auth/telegram.ValidateLogin` already exists, the casino's admin
  uses it) when `Config.TelegramBotToken` is set: it maps to `admin_users.telegram_id` and still
  asks for TOTP.
- Session: 32 random bytes in an `HttpOnly; Secure; SameSite=Strict` cookie, stored as a SHA-256
  hash in `admin_sessions (id, user_id, hash, created_at, seen_at, ip, user_agent)`. Idle timeout
  30 min, absolute 12 h. Disabling a user or changing their role deletes their sessions.
- CSRF: SameSite=Strict, plus every write requires an `X-Admin` header and an `Origin` header
  equal to `Config.Origin`.
- Rate limits: at most 5 failed logins per email in any 15 minutes; further attempts are refused
  (429) until the oldest of those falls out of the window. Every login attempt (ok or not) goes in
  the audit log.

## Audit log

```sql
CREATE TABLE admin_audit (
    id         bigserial PRIMARY KEY,
    admin_id   bigint      REFERENCES admin_users(id), -- NULL: failed login of an unknown email
    action     text        NOT NULL,   -- "game.limit.set", "user.unlink", "adjust.request", …
    target     text        NOT NULL,   -- "user:42", "game:dice/usd/", "adjust:7"
    before     jsonb,
    after      jsonb,
    reason     text,                   -- required for money and identity actions
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now()
);
```

Append-only: no update or delete query exists for it. The row is written in the same Postgres
transaction as the change it records. For that, the modules it changes take a transaction:

- `gameconfig.Service.SetGame(ctx, tx pgx.Tx, game string, enabled bool)`,
  `SetLimit(ctx, tx, game, currency, variant string, l LimitOverride)`, `DeleteLimit(ctx, tx, …)`.
  They use the existing `Lock…/Insert…/Update…` queries (written for this), refuse an override that
  loosens a code default, and return before/after for the audit row. After commit, admin publishes
  `gameconfig.Public()` on `gameconfig.Channel`; other instances pick it up within `ReloadEvery`.
- `auth.Users.UnlinkTx(ctx, tx, userID, provider)`. `Unlink` stays and calls it.

All new exported functions; nothing existing changes signature.

## Balance adjustments

The only way the admin moves money. Credit or debit one user in one currency, with a reason.

```sql
CREATE TABLE admin_adjustments (
    id           bigserial PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES users(id),
    currency     text        NOT NULL,
    amount       bigint      NOT NULL CHECK (amount <> 0),  -- minor units; < 0 debits
    reason       text        NOT NULL,
    requested_by bigint      NOT NULL REFERENCES admin_users(id),
    approved_by  bigint      REFERENCES admin_users(id) CHECK (approved_by <> requested_by),
    status       text        NOT NULL CHECK (status IN ('pending_approval', 'approved', 'done', 'failed', 'rejected')),
    error        text,       -- 'failed': e.g. insufficient funds for a debit
    idempotency_key text     NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    done_at      timestamptz
);
```

- New ledger operation, the same shape as `Deposit` and `Reward`:
  `Ledger.Adjust(ctx, c, adjustmentID, userID, amount int64) error`. Transfer ID
  `{nsAdjust << 32, adjustmentID}` with a new namespace `nsAdjust = 6`; a new system account kind
  `kindAdjust` per currency (created by `Init`), a new account code and a new transfer code, all
  appended after the existing constants. A credit debits `kindAdjust`; a debit credits it, and the
  user's account is `NoOverdraft`, so the driver refuses a debit beyond the balance
  (`ErrInsufficientFunds` → status `failed`). No app-side balance check. No driver change, so
  `ledgertest` is untouched; the new operation gets its own ledger tests on both drivers.
- Flow: the request (with the UI's idempotency key) inserts the row and its audit row in one
  transaction. Amount ≤ `ApproveAbove[currency]` (or no threshold set) → `approved` straight away;
  otherwise `pending_approval` until an owner who isn't the requester approves or rejects it (audit
  row each). An `approved` row is applied with `Ledger.Adjust` and then marked `done` or `failed`.
- A crash between the ledger call and marking it done is fixed by a reconciler
  (`a.Reconcile`) that re-applies `approved` rows. The transfer ID makes it safe: TigerBeetle
  semantics return `exists` for the same transfer.
- `kindAdjust`'s balance is the net of all adjustments, shown on the dashboard.

## Bet history from games

Admin can't know each game's tables, and games shouldn't import `admin`. So `app` gets a registry
that a future player-history RPC can use too:

```go
// app
type Bet struct {
	Game     string          `json:"game"`
	ID       int64           `json:"id"`
	UserID   int64           `json:"user_id"`
	Currency ledger.Currency `json:"currency"`
	Amount   int64           `json:"amount"`
	Payout   int64           `json:"payout"`
	Status   string          `json:"status"` // pending, settled, voided
	Detail   json.RawMessage `json:"detail"` // game-specific: dice's target, over, roll
	At       time.Time       `json:"at"`
}

// Bets registers a game's bet history: newest first, keyset by id.
func (a *App) Bets(game string, f func(ctx context.Context, userID, beforeID int64, limit int32) ([]Bet, error))
```

`games/dice` registers one (a new sqlc query on `dice_bets_user`), and `docs/writing-a-game.md`
says to. The player page merges the sources per game (one tab per game; no cross-game merge in v1).
`Detail` must never contain an unrevealed server seed; fairness data comes only from the fairness
module, which shows a bet's seed pair hash and nonce, and the server seed only once revealed
(`fairness.Service.PairsOf(ctx, userID)`, new, returns that public view).

## Brand views

A brand describes a view in Go; the UI draws it from the description. The brand writes no
frontend code, and upgrades never break its views because the renderer ships with gamify.

```go
adm, err := admin.Register(ctx, a, cfg)
err = adm.View(admin.View{
	Name: "crash-rounds", Title: "Crash rounds", Group: "Crash", // URL /views/crash-rounds
	Perm:       admin.PermView,
	UserFilter: "user_id", // also a tab on the player page, filtered to that player
	Columns: []admin.Column{
		{Key: "id", Type: admin.Int},
		{Key: "user_id", Type: admin.UserID},          // links to the player page
		{Key: "crash_at", Type: admin.Decimal, Scale: 2}, // 237 shows as 2.37
		{Key: "pot", Type: admin.Money, CurrencyKey: "currency"},
		{Key: "status", Type: admin.Enum, Options: []string{"open", "settled", "voided"}},
		{Key: "created_at", Type: admin.Time},
	},
	Filters: []admin.Filter{{Key: "user_id", Type: admin.Int}, {Key: "status", Type: admin.Enum, Options: …}},
	List:    func(ctx context.Context, q admin.Query) (admin.Page, error) { … }, // keyset: q.Before, q.Limit
	Get:     func(ctx context.Context, id int64) (admin.Row, error) { … },    // optional detail page
	Actions: []admin.Action{{
		Name: "void", Title: "Void round", Perm: "crash.void", Reason: true,
		Confirm: "Void this round? Players get their stakes back.",
		Fields:  []admin.Field{…}, // optional inputs, same types as columns
		Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (before, after any, err error) { … },
	}},
})
```

- Field types: `Text`, `Int`, `Decimal` (an integer shown with `Scale` decimals, never a float),
  `Money` (minor units; currency from the row's `CurrencyKey` or a fixed `Currency`), `Bool`,
  `Time`, `Enum`, `UserID`, `JSON`. A `Row` is `map[string]any`; `Page` is `{Items []Row, Next
  *int64}`, the same keyset shape as our endpoints.
- `View` validates at registration and returns an error (unknown permission, duplicate name, a
  `Money` column without a currency, a filter key not among the columns, …). Call it after
  `Register` and before `Run`.
- Actions run inside the admin's transaction: admin opens it, calls `Run`, writes the audit row
  (action `view.<view>.<action>`, target `<view>:<id>`, `Run`'s before/after, the reason) and
  commits. An error from `Run` rolls everything back. `Call` carries the row ID, the validated
  inputs and the acting console user.
- Actions never move money directly: the ledger isn't in that transaction. An action that needs
  money moved changes the game's own row (mark the round void) and the game's reconciler does the
  ledger part, the same as a bet after a crash. Credits and debits by hand stay adjustments.
- Permissions: a view or action uses a built-in permission or one the brand defines in
  `Config.Perms map[admin.Perm][]admin.Role` (`"crash.void": {RoleOperator}`). Owners hold every
  permission. A brand permission can't reuse a built-in name. The UI shows a view only to those
  who hold its permission, and the server checks it on every call.
- Worked example: `games/dice/diceadmin` registers a "Dice bets" view (filters user and status,
  player tab). Dice itself doesn't import `admin`; a brand's game follows the same split
  (`docs/writing-a-game.md`).

## HTTP API

JSON under `/api`, the UI at `/`. Keyset pagination: `?before=<id>&limit=` (max 100), the response
has `next`. Errors: `{"code": "insufficient_funds", "message": "…"}` with a 4xx status.

| Method | Path | Permission |
|---|---|---|
| POST | `/api/login`, `/api/login/telegram`, `/api/totp/enrol`, `/api/logout` | none |
| GET | `/api/me` (user, permissions, brand) | any session |
| GET | `/api/dashboard` (online count, system balances per currency, rewards paid, adjust net) | view |
| GET | `/api/users?q=&before=` (`auth.Users.Search`) | view |
| GET | `/api/users/{id}` (user, identities, balances) | view |
| GET | `/api/users/{id}/bets?game=&before=` | view |
| GET | `/api/users/{id}/seeds` (only with `Config.Fairness`) | view |
| DELETE | `/api/users/{id}/identities/{provider}` (reason) | identity |
| POST | `/api/users/{id}/disconnect` (`Realtime.Disconnect`) | identity |
| GET | `/api/games` (code caps, overrides, effective limits) | view |
| PUT/DELETE | `/api/games/{game}`, `/api/games/{game}/limits/{currency}/{variant}` | limits |
| GET/POST | `/api/adjustments`, `POST /api/adjustments/{id}/approve`, `/reject` | view / adjust / approve |
| GET/POST/PATCH | `/api/admins`, `/api/admins/{id}` (role, disable, reset TOTP) | admins |
| GET | `/api/audit?admin=&action=&target=&before=` | view |
| GET | `/api/views` (descriptions of the views this user may see; no functions) | any session |
| GET | `/api/views/{name}?<filters>&before=&limit=`, `/api/views/{name}/{id}` | the view's |
| POST | `/api/views/{name}/{id}/actions/{action}` (`{reason, input}`) | the action's |

New `ledger` and `auth` reads the dashboard and player page need: `Ledger.SystemBalances(ctx, c)`
and `auth.Users.Identities(ctx, userID)` (provider, subject, linked_at).

## UI

- `admin/ui`: Vite + React + TypeScript + Refine + shadcn/ui, pnpm. A custom Refine data provider
  for the API above (keyset cursors, not offsets) and an auth provider for the cookie session and
  `/api/me`; Refine's access-control provider reads the permissions.
- The build output `admin/ui/dist` is committed and embedded (`//go:embed ui/dist`), so `go get`
  works without Node. CI rebuilds it and fails if the committed copy differs.
- White-label: `Config.Brand` (name, logo, accent colour) comes from `/api/me`; the accent becomes
  CSS variables on shadcn's theme. Light and dark mode.
- Money is shown and entered in minor units converted with the currency's decimals: add
  `Decimals uint8` to `ledger.CurrencyConfig` (0 = show as an integer, today's behaviour). Entry is
  parsed as a decimal string to an `int64`; never a float.
- Adjustments ask for a confirmation that repeats user, currency, signed amount and reason.

### Look and layout: like Nakama's console

The UI follows the layout and interaction patterns of Nakama's console (the user's choice), built
with Refine and shadcn/ui. We copy the structure, not the code, assets or branding: no Heroic
Labs name, logo or icons, and the accent is the brand's.

- **Shell:** a fixed dark sidebar on the left (brand logo and name at the top, icon + label nav
  items, the current one highlighted in the accent colour, collapsible to icons only), and a light
  content area with a slim top bar: page title / breadcrumbs on the left, the console user's email
  and role with a menu (account, theme, log out) on the right. Dark mode darkens the content area
  too. On phone width the sidebar becomes a drawer.
- **Nav, mapped from Nakama's sections:** Status → *Dashboard*; Accounts → *Players*;
  Configuration → *Games*; plus *Adjustments*, *Audit log*, brand views (grouped by their
  `Group`), and at the bottom *Console users* (Nakama's "Users"). Items the user lacks the
  permission for are hidden.
- **Dashboard (Nakama's Status page):** a row of stat cards (players online, and per currency the
  house, rewards and adjust balances), then a table per currency of all system balances.
- **List pages (Nakama's Accounts list):** a search/filter bar above a dense table, row click
  opens the detail page, "Load more" at the bottom (keyset), empty and loading states.
- **Player detail (Nakama's account page):** a header with display name, ID, created and last
  login, and actions (Disconnect, New adjustment); under it tabs: *Profile* (user fields),
  *Authentication* (linked identities with Unlink), *Wallet* (balances per currency and this
  player's adjustments), *Bets* (one sub-tab per game), *Seeds* (only with fairness), then one tab
  per brand view with `user_filter`.
- **Games (Nakama's Configuration page):** one card per game with its enabled switch; inside, a
  table per currency of variant rows showing code cap, override and effective limit, with an Edit
  dialog.
- **Login:** a centred card with the brand logo, email, password and TOTP code; the TOTP enrol
  step shows the `otpauth` URI as a QR code and the secret as text.
- Dense, data-first styling like Nakama's: small type, compact rows, monospace for IDs, amounts and
  hashes, amounts right-aligned.

## Changes to existing code

| Where | Change |
|---|---|
| `app` | `App.Serve(name, *http.Server)` for extra listeners in `Run`; `App.Bets` registry and `app.Bet` |
| `ledger` | `Adjust`, `nsAdjust`, `kindAdjust` and its codes, `SystemBalances`; `CurrencyConfig.Decimals` |
| `gameconfig` | `SetGame`, `SetLimit`, `DeleteLimit` taking a `pgx.Tx`; `LimitOverride` |
| `auth` | `Users.UnlinkTx`, `Users.Identities` |
| `fairness` | `Service.PairsOf` (public view only) |
| `games/dice` | registers its bet history; new `games/dice/diceadmin` (worked brand-view example) |
| `cmd/gamify` | `ADMIN_ADDR`, `ADMIN_ORIGIN`, `gamify admin add-user` |
| docs | README layout row, `writing-a-game.md` (register bets, add an admin view), CHANGELOG under Unreleased |

All additions; no exported API changes and no edits to committed migrations.

## Tests

- `admin` on `apptest`: every route rejects a session without its permission (table-driven over
  the role map) and rejects writes without `X-Admin`/`Origin`.
- Audit: a limit change that fails validation leaves no audit row; a successful one leaves exactly
  one with before/after.
- Adjustments: a retried request (same idempotency key) creates one row; a debit beyond the balance
  ends `failed` and moves nothing; the requester can't approve their own; the reconciler finishes an
  `approved` row left after a simulated crash, and re-running it doesn't pay twice.
- `Ledger.Adjust` on Postgres and TigerBeetle (integration tests, like the other operations).
- No unrevealed seed: the seeds endpoint for a user with an active pair returns its hash only.
- Login: lockout after 5 failures, TOTP required, a disabled user's session stops working.
- Brand views: a view is hidden from and refused to a role without its permission; an action whose
  `Run` fails leaves neither its change nor an audit row; registration refuses an invalid view.
- UI: a Playwright smoke in `e2e/` (log in, find a player, change a limit), Docker stack as today.

## Order

1. Backend core: tables, console users, login, sessions, RBAC, audit, `add-user`.
2. Read views: dashboard, players, bets (`App.Bets`, dice), audit.
3. Writes: limits (gameconfig tx API), unlink, disconnect.
4. Adjustments: `Ledger.Adjust`, approval, reconciler.
5. Brand views: `admin.View`, brand permissions, `diceadmin`.
6. UI, page by page as the endpoints land, then the generic view renderer; commit `dist`.

## Left to the builder

- argon2id parameters, TOTP library or a small in-tree implementation, session cleanup job.
- Exact error codes, JSON field names, page layouts and component choices within shadcn.
- Whether `Decimals` lands in this change or just before it.
- How the CI check for a stale `dist` is wired.
