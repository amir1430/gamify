# Game-Neutral Platform Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove all game logic (dice) from gamify, make its money API game-neutral, and add Spend, Transfer (with per-currency `Transferable`) and a pot winner cap.

**Architecture:** Delete `games/` first so later renames never touch it. Then the ledger renames, the new primitives (each with `ledgertest` cases run on Postgres and TigerBeetle), then the `app` history and `gameconfig` renames with their admin API/UI, then docs and the UI build.

**Tech Stack:** Go, pgx/v5, sqlc, goose, TigerBeetle 0.16.46, React/Vite admin UI (`admin/ui`, built into committed `admin/ui/dist`).

**Spec:** `docs/superpowers/specs/2026-10-22-game-neutral-platform-design.md`

## Global Constraints

- Nothing tagged, databases fresh: edit committed migrations in place, no upgrade migrations, no `/v2`.
- Transfer IDs, account/transfer code values, leg numbers, `Game` range, currency `Ledger` values: unchanged. Renames are Go identifiers, JSON and SQL column names only.
- `fairness/` is not touched (code, comments, tests, vectors).
- Never weaken a `ledgertest` case; every ledger change passes on Postgres and TigerBeetle.
- Money is `int64` minor units; drivers refuse overdrafts, no app-side balance checks.
- sqlc for every query; never edit `internal/store` by hand, run `sqlc generate`.
- Every user-visible change goes in `CHANGELOG.md` under Unreleased (final state; no upgrade notes).
- macOS: `CGO_LDFLAGS=-Wl,-ld_classic`.

**Test env** (every `go test` below): `docker compose up -d postgres` and `docker compose --profile tigerbeetle up -d tigerbeetle`, then
`export TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable TEST_TB_ADDRESS=3000 CGO_LDFLAGS=-Wl,-ld_classic`
(check `docker-compose.yml` / `.env.example` for the real credentials and port). Tests skip without these vars: a skipped driver test is not a pass.

## Review Focus

1. `Transfer` with `from == to` → error, nothing moves (tested in Task 4).
2. `Transfer` in a non-transferable currency with amount 0 → still `ErrNotTransferable`: the currency check comes before the zero-amount shortcut (Task 4).
3. Same `(g, opID)` used for `SettleInstant`, `Spend` and `Transfer` → three independent money moves, no ID clash (Task 4).
4. Negative amount to `Spend`/`Transfer` → error, nothing moves (Tasks 3, 4).
5. Retry of a `Spend` refused for funds → `ErrAlreadyFailed`, even after a top-up (Task 3).

---

### Task 1: Delete dice

**Files:**
- Delete: `games/` (whole tree)
- Modify: `cmd/gamify/main.go`, `sqlc.yaml:39-46`, `e2e/smoke.mjs`, `admin/read_test.go`, `admin/write_test.go`, `ledger/ledgertest/ledgertest.go:25-31`, `ledger/game_test.go:65-75`, `gameconfig/gameconfig_test.go`, `gameconfig/pvp_test.go`, `app/bets_test.go`, `metrics/metrics_test.go`, `realtime/realtime.go:79`, `admin/migrations/00001_admin.sql:33` (comment), `admin/ui/src/pages/audit.tsx:82,119`, `CLAUDE.md`

**Interfaces:**
- Produces: a repo with no `games/` and no `dice` outside `fairness/`, `docs/`, `gamify platform changes.md`, `admin/ui/dist`, README, CHANGELOG (those last three in Task 8).

- [ ] **Step 1:** `git rm -r games`.
- [ ] **Step 2:** `cmd/gamify/main.go`: drop dice and diceadmin imports and wiring; keep `fairness.Register` and admin wiring; package doc says "the platform without games".
- [ ] **Step 3:** `sqlc.yaml`: remove the `games/dice` entry.
- [ ] **Step 4:** `e2e/smoke.mjs`: remove every `dice.bet` step (lines ~65-83); keep login, wallet and the rest.
- [ ] **Step 5:** `admin/read_test.go`, `admin/write_test.go`: replace dice with a test-local fake game named `"arena"`: register `a.Bets("arena", src)` where `src` serves fixed rows for alice newest first honoring `beforeID`/`limit`, and `a.Games.Game("arena", 1_000_000, 98_000_000, nil)`. Keep every assertion (paging with `limit=1` and `before`, game listing `["arena"]`, limit writes, audit target `game:arena/usd/`, cap violation using the fake's max `1_000_000 + 1`). Remove the `dice.bet` RPC calls.
- [ ] **Step 6:** Rename fixtures, no behaviour change: ledgertest `dice`→`instant`, `mines`→`held`, `bj`→`extra`; game names `"dice"`/`"plinko"` in `gameconfig`, `app`, `ledger/game_test.go` tests → `"arena"`/`"runner"`; `"dice.bet"` in metrics test and realtime comment → `"arena.join"`; audit placeholder and migration comment `game:dice` → `game:arena`.
- [ ] **Step 7:** `CLAUDE.md`: provably-fair rule cites only `fairness/fairness_test.go`; "Postgres tables of games hold bet metadata only" → "game tables hold game metadata only".
- [ ] **Step 8:** Verify: `go build ./... && go vet ./... && go test ./...` all pass; `grep -rni dice --exclude-dir=node_modules --exclude-dir=.git . | grep -v -e '^./fairness' -e '^./docs/' -e 'gamify platform changes' -e admin/ui/dist -e README -e CHANGELOG` prints nothing.
- [ ] **Step 9:** Commit `remove dice: the platform ships no game`.

### Task 2: Ledger renames

**Files:**
- Modify: `ledger/ledger.go`, `ledger/pot.go`, `ledger/game_test.go`, `ledger/ledgertest/ledgertest.go`, `gameconfig/gameconfig.go:213`, `cmd/gamify/main.go:188-211`

**Interfaces:**
- Produces: `func (l *Ledger) Hold(ctx context.Context, g Game, c Currency, opID, userID, amount int64) error`; `SettleInstant`/`SettleHeld`/`HoldExtra`/`SettleHolds` same names, params `opID`, `amount`; `CurrencyConfig.MinAmount int64`; `func (l *Ledger) MinAmount(c Currency) int64`; unexported `opTransferID`, `codeCharge` (= 10).

- [ ] **Step 1:** Renames per spec §2: `HoldStake`→`Hold`, `betID`→`opID`, `stake`→`amount`, `betTransferID`→`opTransferID`, `MinBet`→`MinAmount` (field, method, `New` error text "min amount must be > 0"), `codeStake`→`codeCharge`. Delete `Wins`. Rewrite doc comments naming Dice/Mines/Blackjack/Keno/bets to the shape ("an outcome known at once", "a multi-step game", "operation"). `Game` doc: "an ID space for one table of operation IDs; a module with two such tables registers two".
- [ ] **Step 2:** Update callers: `gameconfig` (`ledger.MinAmount`), `cmd/gamify` `parseCurrencies` (field and the `minamount` word in its doc and error text), ledgertest (`MinAmount:` in `USD`/`Tickets`, `Hold`), `ledger/game_test.go` map key `"Hold"`.
- [ ] **Step 3:** Verify: `go build ./... && go vet ./... && go test ./ledger/... ./gameconfig/... ./cmd/...` pass, both driver tests run (not skipped). `grep -rn "HoldStake\|betTransferID\|MinBet\|codeStake\|ledger.Wins" --include='*.go' .` finds only gameconfig's own `MinBet` (Task 7).
- [ ] **Step 4:** Commit `ledger: neutral names (Hold, opID, amount, MinAmount)`.

### Task 3: Spend and the sales account

**Files:**
- Modify: `ledger/ledger.go`, `ledger/ledgertest/ledgertest.go` (ledgerSuite), `ledger/game_test.go`, `admin/ui/src/pages/dashboard.tsx:8`

**Interfaces:**
- Consumes: Task 2 names.
- Produces: `func (l *Ledger) Spend(ctx context.Context, g Game, c Currency, opID, userID, amount int64) error`; `codeSales uint16 = 19`, `codeSpend uint16 = 20`, `nsSpend uint64 = 7`, `kindSales` (next after `kindAdjust`, `systemKinds` name `"sales"`); helper `func nsOpID(ns uint64, g Game, leg uint64, opID int64) ID { return ID{ns<<32 | uint64(g)<<8 | leg, uint64(opID)} }` (Task 4 uses it too).

- [ ] **Step 1: Failing ledgertest case** `t.Run("spend", …)` in ledgerSuite, game `shop ledger.Game = 7`:
  - user with 1000 usd: `Spend(shop, "usd", op, u, 300)` twice → balance 700; `SystemBalances(usd)["sales"]` grew by 300.
  - `Spend(..., 0)` → nil, nothing moves.
  - `Spend(..., 701)` → `ErrInsufficientFunds`; after `TopUp(usd, u, 1000)`, the same opID again → `ErrAlreadyFailed`.
  - `Spend(..., -1)` → error, balance unchanged.
  - tickets user (1000 welcome): `Spend(shop, "tickets", op, u, 1000)` succeeds → 0 (welcome may be spent).
  - unknown currency `"eur"` → error.
  Add `"Spend"` to the game-check table in `ledger/game_test.go`.
- [ ] **Step 2:** Run `go test ./ledger/...` → FAIL (`Spend` undefined).
- [ ] **Step 3:** Implement `Spend` (game check, amount 0 → nil, currency lookup, one transfer user → `system(cur, kindSales)`, ID `nsOpID(nsSpend, g, 1, opID)`, code `codeSpend`, via `createTransfers`). Add `kindSales: codeSales` to `Init` (not `NoOverdraft`, like house). Negative amount: reject with an error before building transfers (`uint64` of a negative would read as `AmountMax`-ish).
- [ ] **Step 4:** Run `go test ./ledger/...` → PASS on both drivers.
- [ ] **Step 5:** `dashboard.tsx` `ACCOUNTS`: add `'sales'` after `'rewards'`; `LABEL.sales = 'Sales'`.
- [ ] **Step 6:** Commit `ledger: Spend into a per-currency sales account`.

### Task 4: Transfer and Transferable

**Files:**
- Modify: `ledger/ledger.go`, `ledger/ledgertest/ledgertest.go`, `ledger/game_test.go`, `cmd/gamify/main.go` (`parseCurrencies`), its test if present

**Interfaces:**
- Consumes: `nsOpID` (Task 3).
- Produces: `CurrencyConfig.Transferable bool`; `var ErrNotTransferable = errors.New("ledger: currency not transferable")`; `func (l *Ledger) Transfer(ctx context.Context, g Game, c Currency, opID, from, to, amount int64) error`; `codeTransfer uint16 = 21`, `nsTransfer uint64 = 8`; legs 1 transfer, 2 floor, 3 back.

- [ ] **Step 1: Failing ledgertest case** `t.Run("transfer", …)`, game `gift ledger.Game = 8`; set `Tickets.Transferable = true`, `USD` stays false:
  - `Transfer(gift, "usd", op, a, b, 10)` and amount 0 → `ErrNotTransferable`.
  - a buys tickets (`Swap usd 1000 → tickets 1500`, so 2500 tickets, 1000 welcome): `Transfer(gift, "tickets", op, a, b, 1500)` twice → a 1000, b 2500.
  - `Transfer(..., a, b, 1)` (would dip into welcome) → `ErrInsufficientFunds`, balances unchanged.
  - fresh user with only welcome: transfer 1 → `ErrInsufficientFunds`.
  - `from == to` → error, nothing moves. Negative amount → error.
  - same `(g, opID)` reused: `SettleInstant(gift, "usd", op, u, 100, 0)`, `Spend(gift, "usd", op, u, 100)`, `Transfer(gift, "tickets", op, u2, u, 100)` each move money (no clash).
  Add `"Transfer"` to the game-check table.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement. Order of checks: game, currency known, `Transferable` (→ `ErrNotTransferable`), `from == to`, amount < 0 → error, amount 0 → nil. Transfers: from → to (leg 1); if `Welcome > 0`, linked pair from → floor → from of `Welcome` (legs 2, 3, codes as `HoldPot`'s floor pair); `link` the batch; `createTransfers`.
- [ ] **Step 4:** Run `go test ./ledger/...` → PASS on both drivers.
- [ ] **Step 5:** `parseCurrencies`: accept a sixth field `code:ledger:minamount[:welcome[:decimals[:transferable]]]`, `t` → true, empty/omitted → false, anything else → error. Test (add `cmd/gamify/main_test.go` if none): `"gems:3:1:0:0:t"` → `Transferable: true`; `"gems:3:1:0:0:x"` → error; `"usd:1:1"` → false.
- [ ] **Step 6:** Commit `ledger: Transfer between users, opt-in per currency`.

### Task 5: Pot winner cap

**Files:**
- Modify: `ledger/pot.go`, `ledger/pot_test.go` or ledgertest potSuite

**Interfaces:**
- Produces: `const MaxPotWinners = 255 - legPotWinner` (247); `var ErrTooManyWinners = errors.New("ledger: too many pot winners")`.

- [ ] **Step 1: Failing case** in potSuite `t.Run("winner cap", …)`: a pot with 248 one-unit winners (entries sized so `won == pot-fee`) → `errors.Is(err, ledger.ErrTooManyWinners)` and every entry still held (balances unchanged, `VoidPot` then returns them). Pure-Go check in `pot_test.go` that `MaxPotWinners == 247`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** In `SettlePot`, return `ErrTooManyWinners` (wrapped with the pot ID) when `len(winners) > MaxPotWinners`, before any transfer.
- [ ] **Step 4:** Run `go test ./ledger/...` → PASS on both drivers.
- [ ] **Step 5:** Commit `ledger: cap pot winners below the leg overflow`.

### Task 6: Game history (`app.Activity`)

**Files:**
- Rename: `app/bets.go` → `app/history.go`, `app/bets_test.go` → `app/history_test.go`
- Modify: `app/app.go:32,76-77,128`, `admin/read.go:93,163-191`, `admin/admin.go:66`, `admin/read_test.go`, `admin/write_test.go`, `admin/ui/src/pages/player.tsx`

**Interfaces:**
- Produces: `type Activity struct` (same fields/JSON as `Bet`); `type HistorySource func(ctx context.Context, userID, beforeID int64, limit int32) ([]Activity, error)`; `func (a *App) History(game string, f HistorySource)` (panics on duplicate, message `app: history of %q registered twice`); `func (a *App) HistorySources() map[string]HistorySource`; route `GET /api/users/{id}/activity`.

- [ ] **Step 1:** Rename in tests first (`history_test.go`, admin tests call `/activity`); run → FAIL to compile.
- [ ] **Step 2:** Rename types, methods, fields (`betsMu`→`historyMu`, `bets`→`history`), route and handler (`adm.activity`); comments "unfinished operations". `PermView` comment: "players, activity".
- [ ] **Step 3:** `player.tsx`: tab `activity` labelled "Activity", URL `/api/users/${id}/activity` and `users/${id}/activity`, column "Amount", empty text "No activity yet.", "No game reports activity."; type `Activity`.
- [ ] **Step 4:** Run `go test ./app/... ./admin/...` → PASS; `npm --prefix admin/ui run build` succeeds (typecheck).
- [ ] **Step 5:** Commit `app: game activity history replaces bets`.

### Task 7: `gameconfig` neutral names

**Files:**
- Modify: `db/migrations/00002_game_config.sql`, `db/queries/gameconfig.sql`, regenerate `gameconfig/internal/store`, `gameconfig/gameconfig.go`, `gameconfig/admin.go`, `gameconfig/*_test.go`, `admin/read.go:217-248`, `admin/write.go:63,165-176`, admin tests, `admin/ui/src/pages/games.tsx`

**Interfaces:**
- Produces: `Limits{Enabled, MinAmount, MaxAmount, MaxPayout}` JSON `enabled`/`min_amount`/`max_amount`/`max_payout`; `Game.MaxAmount`; `func (s *Service) Game(name string, maxAmount, maxPayout int64, variants []string) *Game` (and `PvPGame`); `func (g *Game) Check(c ledger.Currency, variant string, amount int64) (ledger.Currency, Limits, error)`; `LimitOverride{Enabled, MinAmount, MaxAmount, MaxPayout}`; `ErrInvalid` message `"invalid amount"` (code 1100).

- [ ] **Step 1:** Rename in tests (gameconfig and admin: `"max_amount": 500`, audit after contains `"max_amount": 500`, `Check`); run → FAIL to compile.
- [ ] **Step 2:** Migration columns `min_amount`, `max_amount` (CHECK constraints follow); queries; `sqlc generate`.
- [ ] **Step 3:** Rename Go fields/methods/JSON per spec §5, `ErrBadOverride` doc text, package doc ("accepts play", "play path"). Admin read/write JSON keys `min_amount`/`max_amount`, game view `"max_amount"`.
- [ ] **Step 4:** `games.tsx`: types and keys `min_amount`/`max_amount`, labels "Min amount"/"Max amount", "Takes bets" → "Open", "Off stops new bets here" → "Off stops new play here".
- [ ] **Step 5:** Run `go test ./gameconfig/... ./admin/... ./app/...` → PASS; `sqlc generate` leaves no diff; UI build succeeds.
- [ ] **Step 6:** Commit `gameconfig: amount limits instead of bet limits`.

### Task 8: Docs, UI build, final check

**Files:**
- Modify: `docs/writing-a-game.md`, `README.md`, `CHANGELOG.md`, `admin/ui/dist/**` (rebuilt)

- [ ] **Step 1:** Rewrite `docs/writing-a-game.md` with inline snippets (no repo example): pick a `Game` ID and the one-table-per-`Game` rule (pots' entries and pots share one safely); own tables (migrations, sqlc, metadata only); idempotency key + `UNIQUE (user_id, idempotency_key)`; record-then-move-money with `SettleInstant`/`Hold`…`SettleHolds`; pots; non-casino flows: a shop with `Spend` under its own `Game`, gifts with `Transfer` and `Transferable`, a runner granting coins with `Reward`; reconcile; `app.History`; `gameconfig.Game`/`Check` (games without money use only `Enabled`); testing with `apptest`.
- [ ] **Step 2:** `README.md`: drop `games/dice` row and dice references; "Adding a game" points at the guide; `CURRENCIES` format `code:ledger:minamount[:welcome[:decimals[:transferable]]]`; ledger row lists spend and transfers.
- [ ] **Step 3:** `CHANGELOG.md` Unreleased: describe the final state (no sample game; neutral ledger names; `Spend`, `sales` account; `Transfer`, `Transferable`, `ErrNotTransferable`; `MaxPotWinners`/`ErrTooManyWinners`; `app.Activity`/`History`, `/api/users/{id}/activity`; gameconfig `min_amount`/`max_amount`, `Check`; `CURRENCIES` sixth field). Remove entries that mention dice or old names.
- [ ] **Step 4:** `make admin-ui`, then `make admin-ui-check` passes after committing dist.
- [ ] **Step 5:** Final verification: `go build ./... && go vet ./... && go test ./...` (drivers not skipped); `sqlc generate && git diff --exit-code`; the Task 1 grep (now without the README/CHANGELOG/dist exclusions) prints nothing; `node e2e/smoke.mjs` against `make up` passes.
- [ ] **Step 6:** Commit `docs: writing a game without a sample; changelog`.
