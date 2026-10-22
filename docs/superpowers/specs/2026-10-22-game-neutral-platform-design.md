# gamify: a game-neutral platform

Oct 22, 2026

## Intent

gamify started as a casino core. It should host any game: casino, battle-and-build, an endless
runner like Subway Surfers, and so on. The platform keeps what every game needs (identity, money,
realtime, admin, ops, randomness). Each game, casino or not, brings its own rules and decides what
random numbers mean. The platform holds no game logic.

Nothing is tagged yet and no database or TigerBeetle cluster has data to keep (owner confirmed:
fresh project), so APIs, names and committed migrations change in place, with no `/v2`, no upgrade
migrations and no reset step.

## Goals

1. **No game in the platform.** `games/dice` (and `games/dice/diceadmin`) is deleted entirely. No
   example game moves elsewhere in the repo.
2. **Neutral money vocabulary.** The platform's exported API, JSON and tables stop talking about
   bets and stakes. Money moves are described as amounts, holds, payouts, operations.
3. **Money primitives for non-casino games.** Add *spend* (user pays the brand for something: a
   shop item, an extra life) and *user-to-user transfer* (gifts, trades) next to what exists
   (instant charge-and-pay, hold-then-settle, extra holds, pots and pools, grants/rewards,
   deposits, swaps, adjustments).

## Non-goals

- **Randomness.** `fairness/` stays exactly as it is: code, comments, tables, vectors. Its
  casino-flavoured helpers (`SlideWindow`, `Hash512`, `Float4`, round and pot hashes) stay. A shared
  per-match random source is a later project.
- Inventory or items (what a user owns after a spend), leaderboards, matchmaking. Each is its own
  later project.
- New player RPCs. Spend and transfer are Go API calls a game or brand module wraps in its own RPC
  (with idempotency key), as games already do for holds and settles.

## Anti-goals

- Weakening any ledger rule: driver-refused overdrafts, deterministic transfer IDs, the
  `ledgertest` contract on Postgres *and* TigerBeetle, welcome grants staying play-only.
- Changing an existing transfer ID layout, account/transfer code value, `Game` range or a
  currency's `Ledger`. Renames are Go identifiers only; the numbers on the wire stay.
- Leaving dangling references to dice (imports, docs, tests, sqlc config, smoke test).

## Constraints (from CLAUDE.md, unchanged)

Integer minor units, `math/big` for multiplier math, ledger driver is the balance source of truth,
sqlc for every query, a module owns its tables, interfaces only where a second implementation
exists, user identity from the connection only, every money-moving RPC carries an idempotency key.

## Decisions

### 1. Delete dice

Remove `games/` entirely. Everything that imported or mentioned dice changes:

| Where | Change |
|---|---|
| `cmd/gamify/main.go` | Sample brand without games. Keeps `fairness.Register` and admin wiring to show how they're wired. |
| `sqlc.yaml` | Drop the dice package entry. |
| `e2e/smoke.mjs` | Drop the `dice.bet` steps; keep login, wallet and whatever else doesn't need a game. |
| `admin/read_test.go`, `admin/write_test.go` | Replace dice with a test-local fake: an `app.History` source (§4) returning fixed rows and a `gameconfig.Game` registered directly. No RPCs needed. |
| `gameconfig/*_test.go`, `app/bets_test.go`, `ledger/game_test.go`, `ledger/ledgertest`, `metrics/metrics_test.go` | Rename `dice`/`plinko`/`mines`/`bj` fixtures to neutral names (`"g1"`, `"arena"`, …). Behaviour and assertions unchanged. |
| `realtime/realtime.go`, `admin/migrations/00001_admin.sql`, `admin/ui/src/pages/audit.tsx` | Example strings `dice.bet`, `game:dice/…` → neutral (`arena.join`, `game:arena/…`). Migration comment edit is allowed (unreleased). |
| `README.md`, `docs/writing-a-game.md` | Dice was the worked example. Rewrite the guide with inline code snippets for each step (register ID, own tables, idempotency, record-then-move-money, reconcile, history, limits, testing with `apptest`). Cover a non-casino flow (a shop spend, a match pot) alongside a casino one. |
| `CLAUDE.md` | Drop the `games/dice/dice_test.go` vectors mention and the "bet metadata" wording (→ "game tables hold game metadata only"). |
| `CHANGELOG.md` | Unreleased section describes the final state: no sample game, neutral names, Spend, Transfer. No upgrade notes (nothing released). |

`docs/superpowers/specs|plans` from earlier work and `gamify platform changes.md` are history: leave
them.

### 2. Ledger renames

Go identifiers only; transfer IDs, codes and leg numbers keep their values.

| Today | New |
|---|---|
| `HoldStake(g, c, betID, userID, stake)` | `Hold(g, c, opID, userID, amount)` |
| `SettleInstant`, `SettleHeld`, `HoldExtra`, `SettleHolds` | names stay; params `betID` → `opID`, `stake` → `amount` |
| `betTransferID` | `opTransferID` |
| `CurrencyConfig.MinBet`, `Ledger.MinBet` | `MinAmount` (smallest real-money amount a game may charge) |
| `ledger.Wins` | deleted (only dice used it) |
| `codeStake` constant | `codeCharge` (value 10 unchanged) |
| `PotEntry` doc "stake" | "amount" |
| Doc comments naming Dice/Mines/Blackjack/Keno | describe the shape instead ("an outcome known at once", "a multi-step game") |

`Game` keeps its meaning and range, documented more precisely as **an ID space for one table of
operation IDs**. A module with two tables of money-moving rows (a game with matches and a shop)
registers two `Game` IDs, so their row IDs never collide.

### 3. New ledger primitives

Both live in `ledger.go`, follow the existing pattern (game check, currency lookup,
`createTransfers`, a zero amount moves nothing) and get `ledgertest` cases that run on both drivers.

**`Spend(ctx, g, c, opID, userID, amount)`**: user pays the brand. One transfer user → a new
system account `sales` (transfer ID below). Overdraft refused by the
driver. Welcome balance may be spent (spending is play, not cash-out), so no floor check.

**`Transfer(ctx, g, c, opID, from, to, amount)`**: user pays user. Transfer `from` → `to`. Since
it's a cash-out path (farm welcome grants on many accounts, send to one), it keeps welcome
play-only exactly like `HoldPot`: in a currency with `Welcome > 0`, a linked pair `from` → floor →
`from` of `Welcome` follows, so the batch fails if less than the grant would be left. `from == to`
is an error.

**`CurrencyConfig.Transferable`** (bool, default false, decided by the owner): `Transfer` in a
currency without it returns a new `ErrNotTransferable` before any driver call, so real money (usd)
can't move between players unless the brand opts in, typically for soft coins or gems. ledgertest
covers both a transferable and a non-transferable currency. `cmd/gamify` `CURRENCIES` gains an
optional sixth field: `code:ledger:minamount[:welcome[:decimals[:transferable]]]`, `transferable`
= `t` or omitted; README and CHANGELOG follow.

New numbers (none reused):

| What | Value |
|---|---|
| account code `codeSales` | 19 |
| transfer codes `codeSpend`, `codeTransfer` | 20, 21 |
| system account kind `kindSales` | next after `kindAdjust`, named `"sales"` in `SystemBalances` |
| transfer ID namespaces `nsSpend`, `nsTransfer` | 7, 8 |

**Transfer IDs (my call).** Spend and Transfer don't use the game leg space (`g<<8 | leg`), where
`legPotWinner+i` already reaches any leg. They get their own namespaces, keyed by game so two games'
row IDs still never collide: `ID{ns<<32 | uint64(g)<<8 | leg, opID}` with `ns` = `nsSpend` (leg 1)
or `nsTransfer` (legs 1 transfer, 2 floor, 3 back). `Game` ≤ `MaxGame` (24 bits) keeps it below
bit 32.

**Pot winner cap (my call, existing bug).** `legPotWinner+i` overflows the 8-bit leg into the next
game's IDs at 248 winners. `SettlePot` refuses more than `255 - legPotWinner` winners with an
error, before any transfer. New `ledgertest` case on both drivers.

Rule for the guide: one `Game` per table of operation IDs; pot entries and pots of one game share
it safely because their legs are disjoint.

`Init` creates the `sales` account per currency like the other system accounts. The admin dashboard
lists accounts by hand (`admin/ui/src/pages/dashboard.tsx` `ACCOUNTS`): add `sales` there and
rebuild `admin/ui/dist`.

### 4. Game history in `app`

| Today | New |
|---|---|
| `app.Bet` | `app.Activity` (same fields: game, id, user, currency, amount, payout, status, detail, at) |
| `app.BetSource` | `app.HistorySource` |
| `App.Bets(game, f)` / `App.BetSources()` | `App.History(game, f)` / `App.HistorySources()` |
| `GET /api/users/{id}/bets` | `GET /api/users/{id}/activity` |

`app/bets.go` → `app/history.go`. A game without money returns rows with amount and payout 0 and
puts its own fields (score, level) in `detail`. The admin UI player page follows the rename.
`ReconcileEvery` and `Reconcile` comments say "unfinished operations", not bets.

### 5. `gameconfig` neutral names

Same behaviour (enable/disable per game and variant, per-currency limits clamped to code defaults
and `ledger.MinAmount`, `Off`, `PvPGame`), new names:

| Today | New |
|---|---|
| `Limits.MinBet`, `MaxBet`, `MaxPayout` | `MinAmount`, `MaxAmount`, `MaxPayout` |
| `Game.MaxBet`, `Service.Game(name, maxBet, …)` | `MaxAmount`, `Game(name, maxAmount, …)` |
| `Game.Stake(c, variant, amount)` | `Game.Check(c, variant, amount)` |
| `LimitOverride` fields, JSON `min_bet`/`max_bet` | `min_amount`/`max_amount` (admin API, audit before/after, `games.config`, admin UI) |
| `ErrInvalid` message "invalid bet" | "invalid amount" (code 1100 unchanged) |
| `db/migrations/00002_game_config.sql` columns `min_bet`, `max_bet` | `min_amount`, `max_amount`, edited in place; rerun `sqlc generate` |
| package doc "takes new bets", "bet path" | "accepts play", "play path" |

A game that never charges money only uses `Enabled`; limits don't apply to it.

## Left to the builder

- Order of work and commit split.
- Exact fake used in admin tests, as long as it covers the same assertions dice did (paging,
  game listing, limit writes and audit).
- Wording of doc comments and the rewritten guide.
- Any remaining `bet`/`stake` wording in comments or tests outside `fairness/`: make it neutral.

## Done when

- `grep -rni dice` outside `fairness/`, `docs/superpowers/`, `gamify platform changes.md` and
  `admin/ui/dist` finds nothing; `admin/ui/dist` is rebuilt.
- `go build ./...`, `go vet ./...`, `go test ./...` pass, with `ledgertest` run on Postgres and
  TigerBeetle including the new Spend and Transfer cases.
- `sqlc generate` leaves no diff.
- `e2e/smoke.mjs` passes against `cmd/gamify`.
