# gamify v1.1: multiplayer (matchmaking, parties, leaderboards, tournaments, leagues)

Oct 22, 2026

## Intent

gamify makes online game development easy for any kind of game (CLAUDE.md, "Breadth"). v1.0 gives
a game users, money, realtime RPCs, limits, fairness and an admin console, but anything with more
than one player still has to build its own social and competitive layer. v1.1 ships that layer,
following Nakama's feature set (https://github.com/heroiclabs/nakama), shipped together for
integrity (owner's call):

- **Matchmaking**: put players together, hold their money while they play, settle money and
  ratings on the result.
- **Parties**: groups that play together and message each other.
- **Leaderboards**: as many as needed, dynamic, seasonal, top N and around a player.
- **Tournaments**: compete over prizes, score-based or bracket-based.
- **Leagues**: tournaments chained into seasons, with divisions or with rank reset and placement.

The split stays as in v1.0: **the platform owns matching, money, ratings and rankings; the
brand's game owns the rules.** Tournaments and leagues are run by operators from the admin console
(owner's call), not only from code: a tournament organizer with a Telegram Mini App and no game
code of their own must be able to run "12 teams of 5, knockout, prize pool from entry fees".

## Goals

1. Every match shape: 1v1, free-for-all (2..N), teams (2v2, 5v5), asymmetric teams (1 vs 4),
   parties queueing together, registered teams.
2. Every money mode: free, ranked (ratings), staked (equal stakes, winners take the pot less the
   house fee), and combinations. Prizes from entry fees, from the brand's funds, or both.
3. Platform-held ratings, used by matchmaking and leagues (owner's call).
4. Leaderboards, tournaments and leagues created and run from the admin console, and from Go.
5. Matches played outside gamify (an esports match, a LAN game) work: results reported later.
6. Safe under retries, crashes and several app instances, like every money path in v1.0.

## Non-goals (v1.1)

- A server-side match loop (ticks, authoritative state). Games run on RPCs as today.
- Backfill into running matches, ready checks, different stakes per player in one match.
- Stored chat history, voice, global or group chat outside a party.
- A query language for tickets (Nakama's Bleve queries): declared properties and a Go hook.

Each can be added later without changing the v1.1 API; the design keeps the door open.

## Anti-goals

- Moving money anywhere but through `ledger`, or with a non-deterministic transfer ID.
- Trusting the client for identity, rating, scores, or anything that decides matching beyond the
  properties a queue declares. Scores come from game code or an admin, never straight from a client.
- Breaking any v1.0 exported API or editing a committed migration. Everything is additive.
- Genre-specific code in the platform.
- An operator able to spend the brand's money on prizes without the same approval rule as balance
  adjustments.

## Constraints

CLAUDE.md applies unchanged. What exists to build on:

- `ledger/pot.go`: `HoldPot`, `SettlePot` (≤ `MaxPotWinners`, one linked batch, fixed 5% `Fee`),
  `SettlePool` (big pools, chunked), `VoidPot`. `ledger.Reward` (from the rewards account; claim
  IDs share one namespace across the deployment).
- `gameconfig.PvPGame`: admin on/off and min/max amount per currency.
- `app.App`: `Migrate`, `Router`, `Reconcile` (15 s), `History`, `Realtime.Publish/Online`,
  `realtime.UserChannel`. Centrifugo: clients never publish; only Go does.
- `admin`: `View` (list, detail, row actions; actions never move money), adjustments with
  `Config.ApproveAbove` approval, audit rows.
- `auth/telegram`: Telegram login.

## Policy is config (CLAUDE.md, "No imposed policy")

Every product rule below is brand config with the stated default, settable in Go and, for what
operators run (tournaments, leagues, leaderboards, queues' on/off), in the admin console: sizes,
timeouts, scoring, tie and draw handling, who may report, one-party-per-user, concurrency, fees.
What is not config: ledger semantics and money safety, identity from the connection,
idempotency, fairness, audit. Where this document says "default", the builder exposes a setting.

## Packages

| Package | Owns | Depends on |
|---|---|---|
| `party` | parties, members, party messages | app |
| `matchmaking` | queues, tickets, matches, ratings | app, party, ledger |
| `leaderboard` | boards, seasons, records | app |
| `tournament` | tournaments, entrants, brackets, leagues, prize payouts | app, party, matchmaking, leaderboard, ledger |
| `*/…admin` | console pages per package (`matchmakingadmin`, …) | admin + the package |

Each: own migrations and goose table, `Register(ctx, a, …)`, sqlc. Names are the builder's to
adjust; leagues may be their own package if the builder prefers. Every package that moves money
claims its own platform `ledger.Game` (below `FirstBrandGame`), documented in `ledger.go`; prize
prize payouts use a new additive ledger call `RewardPrize(ctx, g, c, claimID, user, amount)` in its
own namespace (`nsPrize`, keyed by game like `nsSpend`), never `Reward`, whose claim IDs belong to
brands' own tables since v1.0. `ledgertest` cases on Postgres and TigerBeetle (my call).

## 1. Parties

```
party.create {max, name, open}  -> party (caller leads)
party.join   {party_id}         -> party
party.leave  {}                 -> {}
party.kick   {user_id}          -> {}      (leader)
party.lead   {user_id}          -> {}      (leader hands over)
party.send   {kind, data}       -> {}      kind: "chat" | "data"
```

- A party is a group that stays together until disbanded: friends tonight, or a registered
  tournament team. Optional name, no expiry. One party per user (default; config).
- The party ID is the invite (a Telegram deep link, say). `open: false` = joining needs the
  leader's `party.accept {user_id}` (Nakama's closed parties).
- Messages: `party.send` publishes `{"type": "party.message", from, kind, data}` to every member's
  `UserChannel` (no new Centrifugo channel, no subscription rules). `data` ≤ 4 KB, rate-limited
  per user (builder's numbers), not stored. Chat and game data ("ready", "pick map") share it.
- Every membership change pushes `{"type": "party", party}` to each member. Leader leaving hands
  the lead to the oldest member; the last one out deletes the party.
- A party-owned leaderboard row keeps its party ID after the party is deleted (shown as a
  disbanded party).
- A party in a running tournament has its roster locked (no join/leave/kick) until it's out.

## 2. Matchmaking

### Queues

A game registers queues in Go (`mm.Queue(QueueConfig{…})`); a tournament creates its own (§5).

| Field | Meaning |
|---|---|
| `Name` | unique, used in RPCs |
| `Game` | optional `*gameconfig.Game` (a `PvPGame`): admin on/off and stake limits |
| `Teams` | list of `{Min, Max}` players per team. 1v1 `[{1,1},{1,1}]`; 5v5 two `{5,5}`; 1 vs 4 `[{1,1},{4,4}]`; free-for-all: `[{1,1}]` with `FFA: {Min: 2, Max: 8}` |
| `Ranked` | ratings change on result and are used for matching |
| `Stakes` | per currency, allowed stakes (`{"usd": {100, 500}}`); empty = free. Checked with `Game.Check` |
| `FeeBP` | house fee of a pot in basis points (default `ledger.FeeBP`) |
| `Properties` / `Numbers` | string keys that must be equal (`region`); numeric keys with a max difference that widens with wait |
| `Compatible` | optional `func(a, b Ticket) bool` |
| `FillAfter` | wait before forming with fewer than max players (default 10 s) |
| `MaxWait` | ticket expiry (default 2 min; 0 = none) |
| `RequireOnline` | default true: drop tickets whose players are offline (Nakama does on disconnect). False for sign-up flows (a Mini App) and matches played outside gamify |
| `Concurrent` | default false: a player in an open match of this queue can't queue or be put in another one. True for async/turn-based games (several chess games at once) |
| `MatchTimeout` | no result by then: cancelled and refunded (default 1 h; days for external matches) |
| `RatingKey` | rating used and updated (default `Name`; queues may share one) |
| `OnMatch` / `OnFinish` | optional hooks; idempotent (called again on retry). `OnFinish` is how tournaments advance |
| `Payout` | optional `func(pot int64, m Match, r Result) []ledger.PotWin`; default below |

### Tickets

```
match.queue  {queue, currency, stake, properties, numbers, idem_key} -> ticket
match.cancel {}                                                      -> {}
match.status {}                                                      -> {ticket, matches: [...]}
```

- One active ticket per user (party members included), `UNIQUE` in Postgres. A party's leader
  queues the whole party; a party larger than every team is refused.
- `idem_key`s in their own table that outlives tickets: a repeat returns the same ticket, the
  match it became, or why it was removed. Never a second ticket or stake.
- Only declared keys accepted; rating is the platform's. Tickets live in Postgres.
- `match.status` lists matches even when `Concurrent` is false, so the API never changes shape.
- Removed tickets push `{"type": "match.ticket_removed", reason}`.

### Matching

- Every second each instance runs the matcher; per queue `pg_try_advisory_xact_lock` so one
  instance matches a queue at a time.
- Oldest ticket first; gather compatible tickets (currency, stake, properties, numbers, ranked
  rating window widening with wait, `Compatible`). Parties are indivisible, kept on one team.
- Form at max players; after `FillAfter`, at min. Ranked queues balance teams by rating (greedy).
- `// ponytail:` O(n²) per queue per tick; bucket by properties if a queue outgrows it.

### Formation (record first, then money)

1. One transaction: delete tickets, insert `matches` (`forming`) and `match_players`. Commit.
2. Staked: `HoldPot` per player (entry ID = `match_players` ID). A refusal voids every entry,
   cancels the match, drops the short player's unit (`insufficient_funds`) and re-queues the
   others with their original queue time.
3. `OnMatch`; an error cancels (refund).
4. `running`; push `{"type": "match.found", match}`.

The reconciler finishes `forming` matches through the same idempotent steps.

### Direct matches

`mm.Create(ctx, NewMatch{Queue, Teams [][]int64 | Parties []int64, Currency, Stake, Ref})`: same
pipeline without tickets. `Ref` unique per queue (repeat = same match). Used by tournaments and a
brand's "challenge a friend".

### Results and cancel

```go
mm.Finish(ctx, matchID, Result{Ranks: []int{1, 2}}) // rank per team; ties share; a forfeit is a rank
mm.Cancel(ctx, matchID, reason)                     // refund everything
```

- `Finish`: store result, `finishing`, one transaction; then ratings and money; then `settled`.
  Same result again = no-op; a different one = error. Reconciler completes `finishing`.
- Who reports: the game in Go; for external matches a brand RPC (team leaders) or the admin
  console's "report result" action (stores the result and marks `finishing`; actions never move
  money).
- Staked default payout: fee = `FeeBP` of the pot (rounded up); rank-1 players split the rest
  equally, remainder one unit each in `match_players` ID order. Asymmetric teams (1 vs 4) stake
  the same and split the same way unless `Payout` says otherwise. A draw pays the fee (default;
  queue config `DrawFee`, false = a draw refunds).
- Ledger, additive: `SettlePotFee(ctx, g, c, potID, feeBP, entries, winners)`; `SettlePot` =
  `SettlePotFee` with `FeeBP`. Same transfer IDs. `SettlePoolFee` likewise. `ledgertest` cases on
  Postgres and TigerBeetle.
- `Cancel`: `cancelling` → `VoidPot` all → `cancelled` → push. `MatchTimeout` cancels via reconciler.
- Pushes: `match.found`, `match.finished` (result, payouts, rating changes), `match.cancelled`.

### Ratings

- `ratings(user_id, key, mu, sigma, matches, updated_at)`. OpenSkill Plackett-Luce (1v1, FFA,
  teams), mu 25, sigma 25/3, shown = mu − 3σ (my call over Elo, which only does 1v1). Floats are
  fine: not money. In-package or a maintained library, builder's call; pinned reference vectors.
- Updated in the transaction that marks the match `settled`. `rating.get {key}`;
  `mm.Rating(ctx, user, key)`.

## 3. Leaderboards

- Created in Go (`lb.Create`) or the admin console; any number. Config: `id` (stable string),
  `sort` (desc/asc), `operator` (best, set, incr, decr), `owner` (user or party), optional
  `reset` schedule (daily, weekly, monthly, or a cron expression), `metadata`.
- Scores are `int64` plus optional `subscore` (tie-break) and small `metadata` JSON. Ties after
  subscore: earlier submission ranks higher.
- Submitting: `lb.Submit(ctx, board, owner, score, subscore, meta)` from game code; admin console
  can set or remove a record (a cheater) with a reason. No client RPC writes a score.
- Seasons: each reset starts a new season number; rows carry the season, so old seasons stay
  readable as archived standings. No copying.
- Reads (RPCs and Go): `leaderboard.top {board, season?, limit, cursor}`,
  `leaderboard.around {board, owner?, limit}` (default the caller), `leaderboard.get {board, owners}`.
- `// ponytail:` rank = count of better rows on an index (`board, season, score, subscore, at`);
  fine to ~10^6 rows per board. A sorted-set cache is the upgrade path.

## 4. Tournaments

A tournament is created by an operator in the console (or in Go) and has one of two formats.

**Common config** (all of it policy, so all of it editable): name, game (optional), currency, entry fee (0 = free), entrant kind (players or
parties, with team size), max entrants, sign-up window, start, end, optional repeat (a recurring
weekly cup), prize table, prize source, and for paid entries a minimum entrant count (below it,
the tournament cancels and refunds).

- `tournament.list`, `tournament.get {id}`, `tournament.join {id, party_id?, idem_key}`,
  `tournament.leave {id}` (before start; refunds). Joining with an entry fee holds it with
  `HoldPot`: one entry row per paying member (entry ID = that row), pot ID = the tournament. A
  party joins via its leader; each member pays their own entry fee; one short member refuses the
  whole party. Each occurrence of a repeating tournament is its own tournament row and pot.
- Held entries have no ledger timeout: they stay held through multi-day sign-ups and external
  matches until settled or voided.

**Score format** (Nakama's): a leaderboard scoped to the tournament's window; entrants submit
through the game (`t.Submit` → `lb.Submit`), optionally at most N attempts. Only entrants may
submit, free tournaments included (so attempts can be counted). Ranks at `end` decide prizes.

**Bracket format**: single elimination, double elimination, round robin, Swiss (number of rounds
configurable). Seeding by rating, by sign-up order, or random (`crypto/rand`, recorded). Byes for
non-power-of-two counts (12 teams → 4 byes in a single-elimination round of 16). Each round's
matches are created with `mm.Create` on the tournament's own queue (free; money sits at the
tournament level); `OnFinish` advances the bracket. Matches may be played inside a brand game or
outside gamify.

- Ties: elimination formats refuse a tied result by default (operator decides); round robin and
  Swiss allow draws, points 3/1/0, tie-break Buchholz. All config.
- Who reports (config, owner-approved default for no-code tournaments): **leaders report** —
  `tournament.report {match_id, ranks}` by each team's leader (or the player, 1v1); both agree =
  result; disagreement, or a missing report by the round deadline, sends the match to the
  console's Disputes list and the operator decides. Other settings: game only (Go `Finish`),
  operator only. Leader reports are claims, not trusted scores: only agreement or an operator
  makes a result.
- Round deadline with no result and no operator decision: configured default (higher seed
  advances, or both out).

**Prizes**:

- Prize table: rank ranges to a share (basis points) of the pool, or fixed amounts. Ties share
  their ranks' combined prize equally (remainder to the earliest entrant). Shares of ranks nobody
  reached are spread over the ranks that exist, proportionally. A party's prize is split equally
  among its members.
- Entry-fee pool: settled with `SettlePotFee`/`SettlePoolFee` (house fee configurable).
- Fixed amounts paid from entry fees: if the net pool (after the fee) is more than the table pays,
  the rest goes to the house (owner-approved default). Fixed brand-funded amounts for ranks nobody
  reached are not paid; spreading over unreached ranks applies to share-based prizes only.
- **Guarantee** = "at least X": the brand tops up (with `RewardPrize`) only when the net entry pool
  falls short of X; the house fee is still taken from entry fees. **Added on top**: a fixed
  brand-funded amount on top of the pool. Both may be set. (Owner-approved.)
- Approval: funding that could exceed `ApproveAbove` for the currency needs a second admin's
  approval before the tournament opens, as balance adjustments do. Checked against the most the
  brand could pay (full guarantee + added). Enforced in the `tournament` package whoever creates
  it (Go or console), so it takes `ApproveAbove` at `Register`. A repeating tournament is approved
  once for the series; any change to its funding needs a new approval. League season prizes
  likewise (my calls).
- A paid tournament with no eligible winner (everyone forfeits) refunds with `VoidPot`; a free one
  under its minimum entrant count is just cancelled.
- Payout runs in the reconciler after `end` (record first, then money); idempotent per entrant.
- Cancelling a tournament refunds every held entry fee (`VoidPot`) and pays nothing.

## 5. Leagues

A league is a chain of seasons for one game, created in the console. Each season is a leaderboard
season or a set of ranked matches; config picks one of two modes (owner: both are needed):

1. **Divisions** (promotion and relegation): ordered tiers (Bronze, Silver, Gold…), each split
   into groups of at most N. A new player joins the lowest tier's open group on their first score
   or match (no sign-up). At season end, the top X of each group move up, the bottom Y move down,
   the rest stay; groups are rebuilt. Tiers, N, X and Y configurable.
   - Group standings (owner-approved default, config): match-based leagues score points per
     result, 3 win / 1 draw / 0 loss; score-based leagues use the season's leaderboard score.
   - The league's queue pairs only players of the same tier (default, config: same group, same
     tier, or anyone). Needs `QueueConfig` to take a pairing constraint, a Go func of the two
     tickets' users, in v1.1 so it isn't a later API change.
   - Optional entry fee per season, held and paid out like a tournament's.
2. **Reset and place**: at season end every rating (the league's `RatingKey`) or score resets,
   softly (pulled toward the default by a configurable percentage, uncertainty raised) or fully.
   The first K results of the new season are placement; after them, tiers come from rating or
   score thresholds the operator sets.

Both: season length and schedule, optional prizes per tier or rank at season end (same prize
rules and approval as tournaments), archived final standings per season, and RPCs for a player's
tier, group and the standings around them (`league.me`, `league.standings`).

## 6. Admin console

Additive changes to `admin`:

- **Page actions**: actions on a view itself, not a row ("New tournament", "New leaderboard").
- **Repeating rows input**: a field type for lists of rows (prize tables, tiers), so operators
  never type JSON.

Pages: Queues (waiting, oldest wait; on/off through `gameconfig`), Matches (filter; cancel, report
result), Parties (view, disband), Leaderboards (create, edit, standings, set/remove record), Tournaments
(create, edit before start, approve funding, bracket and standings, report result, disputes, cancel),
Leagues (create, tiers, seasons, standings), and player-page tabs (ratings, tournaments, leagues).
Every write audited. New permissions: `matchmaking.cancel`, `matchmaking.report`,
`leaderboard.edit`, `tournament.manage`, `tournament.approve`, `league.manage` (builder may adjust).

## 7. Cross-cutting

- Client error codes: platform range below 1100, a block per package (builder assigns).
- Every push to `realtime.UserChannel(user)` with a `type` field.
- `a.History`: staked matches, tournament entries and prizes appear as player activity.
- Metrics per package in the existing `metrics` style.
- Docs: `docs/writing-a-game.md` gains a multiplayer section (a ranked 1v1, a staked 5v5, a score
  tournament fed by a game, a bracket tournament run from the console with no game code); README
  module table; CHANGELOG Unreleased with upgrade notes (new goose tables, ledger additions,
  admin additions).
- Build order (owner: ship together as v1.1): ledger additions, party, matchmaking + ratings,
  leaderboard, tournament, leagues, admin pages, docs.

## Left to the builder

Table and column details, Go type names, tick interval and widening rates, rate limits, error
code numbers, permission names, metric names, cron library or a hand-written schedule, OpenSkill
in-package or imported, package split for leagues.
