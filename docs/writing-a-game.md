# Writing a game

gamify ships no games. It gives every game the same parts: users and logins, money
(`*ledger.Ledger`), realtime RPCs, limits, the admin console and, for games of chance, provable
fairness. Your game is a Go package in your brand's repo, next to your `main`. It owns its rules
and its tables, registers its RPCs on the app, and moves money only through the ledger. A casino
round, a paid tournament, a battle-and-build match, an endless runner: all of them follow the
same steps.

```go
// your main, after app.New
fair, err := fairness.Register(ctx, a, sealer) // only if a game draws random outcomes
_, err = runner.Register(ctx, a)
_, err = arena.Register(ctx, a, fair)
```

## 1. Pick a game ID, once, forever

```go
const (
	Game     ledger.Game = ledger.FirstBrandGame + 1 // matches (one table of operation IDs)
	ShopGame ledger.Game = ledger.FirstBrandGame + 2 // the arena's shop (a second table)
)
```

A `ledger.Game` is an ID space for **one table** of operation IDs: every transfer ID your game
creates is built from it and the row ID. A module with two tables of money-moving rows (matches
and shop purchases) registers two IDs, so their row IDs never collide. Pots are the one shared
case: a pot's entries and the pot itself may come from two tables under one ID, because their legs
never overlap.

Changing an ID, or reusing another game's, makes retries and reconciliation move money twice. IDs
below `ledger.FirstBrandGame` are the platform's; yours are `FirstBrandGame..MaxGame`. Call
`a.Ledger.RegisterGame(Game, "arena")` first thing in `Register`: a duplicate ID in the deployment
then fails at startup.

## 2. Own your tables

- Migrations in `migrations/*.sql`, embedded, run with `a.Migrate(ctx, migrations, "goose_<game>")`.
  After your first release, never edit a migration: add a new one.
- Queries with sqlc. List the platform's schemas your tables reference in `sqlc.yaml` (`users` is
  in gamify's `db/migrations`, `seed_pairs` in `fairness/migrations`).
- A row holds game metadata only: amount, currency, outcome, payout, status, score. The ledger
  holds the balances. Never store or check a balance yourself; the ledger refuses overdrafts
  (`ledger.ErrInsufficientFunds`).
- Money is `int64` minor units. Never a float. Multiplier math with `math/big`, rounded down.

## 3. Every money move is idempotent

The client sends an idempotency key with every RPC that moves money (`idem_key`, at most 64
bytes). Put `UNIQUE (user_id, idempotency_key)` on the table and insert with
`ON CONFLICT DO NOTHING`; on conflict, return the row that's already there. A retried request
returns the same row and never charges twice.

The user ID comes from the RPC handler's `userID` argument (the authenticated connection), never
from the payload.

## 4. Record first, then move money

1. **Create** the row in one transaction, as `pending`. Commit.
2. **Move money** with the row's ID as the operation ID. Transfer IDs come from `(game, leg, ID)`,
   so calling again for the same row is a no-op.
   - `ledger.ErrInsufficientFunds` or `ledger.ErrAlreadyFailed` (a retry of a refused move: the
     ID stays refused for good, even after a top-up): mark the row `voided`.
   - Any other error: leave it `pending` and return the error.
   - Success: mark it `settled`.
3. **Reconcile**: register `a.Reconcile(s.Reconcile)`. Every 15 s it finishes rows still
   `pending` after a crash or a ledger outage, through the same function.

Pick the ledger call by how money moves in your game:

| Money shape | Calls |
|---|---|
| Outcome known at once (a spin, a coin flip) | `SettleInstant(ctx, g, c, opID, user, amount, payout)` |
| Multi-step (a round with decisions, a single-player run with an entry fee) | `Hold`, more with `HoldExtra`, then `SettleHeld` or `SettleHolds` |
| Players against each other (a duel, a battle with an entry fee, a tournament) | `HoldPot` per entry, then `SettlePot` (at most `ledger.MaxPotWinners` winners), `SettlePool`, or `VoidPot` |
| The brand sells something (a skin, an extra life) | `Spend(ctx, g, c, opID, user, amount)` |
| A player gives to a player (a gift, a trade) | `Transfer(ctx, g, c, opID, from, to, amount)` |
| The game gives something (coins picked up in a run, a daily reward) | `Reward(ctx, c, claimID, user, amount)` |

An amount of 0 moves nothing (demo play).

**A shop** (its own table, its own `Game` ID):

```go
// purchases row inserted as pending with its idempotency key, then:
err := a.Ledger.Spend(ctx, ShopGame, "gems", p.ID, userID, item.Price)
```

The welcome grant may be spent: spending is play, not cash-out.

**Gifts** only work in a currency the brand marked `Transferable` (in `cmd/gamify`, the sixth
`CURRENCIES` field: `gems:3:1:0:0:t`); in any other currency `Transfer` returns
`ledger.ErrNotTransferable`. Real money stays non-transferable unless the brand opts in. A
transfer never dips into the sender's welcome grant.

**An endless runner** that pays coins collected in a run:

```go
// a reward claim row (run ID, coins) inserted as pending at the end of the run, then:
err := a.Ledger.Reward(ctx, "coins", claim.ID, userID, claim.Coins)
```

`Reward` isn't keyed by game: claim IDs share one namespace across the deployment, so take them
from one claims table the brand owns, not from each game's own rows.

## 5. Games of chance: provable fairness

- Take the `*fairness.Service` that `fairness.Register` returns. Draw with
  `fair.Next(ctx, tx, userID, n)` **in the same transaction as the row**: the nonce increment
  and the row commit or roll back together.
- Store the draw's `SeedPairID` and `Nonce` on the row (`UNIQUE (seed_pair_id, nonce)`), so the
  player can verify it after `seed.rotate`.
- Turn the draw into an outcome only with `fairness` functions (`SlideWindow`, `Floats`). No
  `math/rand` anywhere in a game. Publish your outcome formula: players verify with it.
- Never log or return an unrevealed server seed.
- A multi-step game registers `fair.Guard(...)` so the seed can't rotate (and reveal the outcome)
  while a game is live.

## 6. Activity history

Register `a.History("arena", s.History)` so the admin can list what players did. The
`app.HistorySource` lists newest first (`beforeID` keyset, `userID` 0 = every user) and fills
`app.Activity`: the row's amount, payout, status and a `Detail` JSON with your game's own fields
(score, level, outcome). A game without money leaves amount and payout 0. `Detail` never holds the
server seed, nor outcomes the player hasn't seen yet (a live round's future steps).

```go
func (s *Service) History(ctx context.Context, userID, beforeID int64, limit int32) ([]app.Activity, error) {
	rows, err := s.q.ListMatches(ctx, store.ListMatchesParams{UserID: userID, BeforeID: beforeID, Limit: limit})
	// map each row: app.Activity{Game: "arena", ID: r.ID, UserID: r.UserID, Currency: …, Amount: …,
	// Payout: …, Status: r.Status, Detail: json.RawMessage(`{"kills":3}`), At: r.CreatedAt}
}
```

## Public channels

A player's own pushes go to `realtime.UserChannel(userID)`, which every connection is subscribed to.
A shared room (a lobby, a round everyone watches) publishes to its own channel, `"<namespace>:<id>"`
with `a.Realtime.Publish` (or `realtime.Broadcast` for many). Declare the namespace in `gamify.yaml`
so Centrifugo accepts it, with `subscribe: true` for clients to join it:

```yaml
centrifugo:
  namespaces:
    - {name: arena, subscribe: true}
```

Never put a user ID or anything private in a public channel's messages.

## Admin view

A game can add its own page to the admin console, described in Go; the console's UI draws it. Put
it in a `<game>admin` subpackage (`arena/arenaadmin`) that imports `admin` and your game, so the
game itself never imports `admin` and brands without the console don't pull it in. Export from the
game only what the view needs. The brand calls `arenaadmin.Register(adm, s)` after
`admin.Register` and before `a.Run`.

```go
return adm.View(admin.View{
	Name: "arena-matches", Title: "Arena matches", Group: "Arena", Perm: admin.PermView,
	UserFilter: "user_id", // also a tab on the player page
	Columns: []admin.Column{
		{Key: "id", Type: admin.Int},
		{Key: "user_id", Type: admin.UserID},
		{Key: "pot", Type: admin.Money, CurrencyKey: "currency"},
		{Key: "status", Type: admin.Enum, Options: []string{"open", "settled", "voided"}},
	},
	Filters: []admin.Filter{{Key: "user_id", Type: admin.UserID}},
	List:    func(ctx context.Context, q admin.Query) (admin.Page, error) { … }, // keyset: q.Before, q.Limit
	Actions: []admin.Action{{Name: "void", Title: "Void match", Perm: "arena.void", Reason: true,
		Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (before, after any, err error) { … }}},
})
```

- Field types: `Text`, `Int`, `Decimal` (an integer shown with `Scale` decimals, never a float),
  `Money` (minor units; currency from the row's `CurrencyKey` or a fixed `Currency`), `Bool`,
  `Time`, `Enum` (`Options`), `UserID` (links to the player), `JSON`. Filters are columns of the
  same type; action inputs (`Fields`) use the same types, `Money`/`Decimal` as integers.
- `View` checks the description and returns an error; register views at startup.
- Actions never move money: the ledger isn't in `Run`'s transaction. Change your game's row (mark
  the match void) and let your reconciler do the ledger part, as for a row left by a crash. The
  console writes the audit row in the same transaction; an error or a panic rolls both back.
- `Call.Reason` is the trimmed reason (`Reason: true`; the audit row holds it too). Return
  `admin.BadInput(msg)` (400), `admin.Conflict(msg)` (409) or `admin.ErrNotFound` (404) for what an
  operator can get wrong; any other error is a logged 500. `Action.After` runs after the commit.
- Permissions: a built-in (`admin.PermView`, …) or one of the brand's in
  `admin.Config.Perms` (`"arena.void": {admin.RoleOperator}`); owners hold them all. Name yours
  `<game>.<verb>` so they can't clash with a built-in.

## 7. Limits and errors

- Declare your limits with `a.Games.Game("arena", maxAmount, maxPayout, variants)`; admins can
  lower them. Check every new amount with `Check` before creating the row; reject one that could
  pay more than the limit, never cap it silently. A game that never charges money only uses the
  admin's on/off switch (`Limits` returns `gameconfig.ErrUnavailable` when it's off).
- Client errors are `*realtime.ClientError` with codes from 1100 up; reuse
  `gameconfig.ErrInvalid` (1100) for bad requests. Any other error reaches the client as a generic
  internal error.

## 8. Test it through its RPCs

`apptest` runs a real App in a test: a fresh Postgres database per test (dropped afterwards), the
Postgres ledger and an in-memory transport instead of Centrifugo. Set `TEST_DATABASE_URL` to any
database on a Postgres server you can create databases on; tests skip without it.

```go
func TestArena(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	if _, err := arena.Register(ctx, a.App); err != nil {
		t.Fatal(err)
	}
	alice := a.User("alice")
	a.Fund(alice, "usd", 1_000)
	res, cerr := apptest.Call[arena.JoinResult](a, alice, "arena.join", arena.JoinRequest{…})
	// a.Balance, a.Pushes(realtime.UserChannel(alice)), a.ReconcileNow()
}
```

Test at least: a request without money (`ErrInsufficientFunds` path), a win and a loss against
`a.Balance`, a retry with the same idempotency key charging once, a row left pending finished by
`a.ReconcileNow()`, and (games of chance) the revealed seed reproducing the outcome.

## 9. Multiplayer: parties and matchmaking

`party` (groups with a leader) and `matchmaking` (queues, matches, ratings, staked pots) are opt-in
modules. Matchmaking forms and settles matches; your game plays them and reports the result.

```go
// your main, after app.New
parties, err := party.Register(ctx, a, party.Config{})              // before matchmaking
mm, err := matchmaking.Register(ctx, a, parties, matchmaking.Config{}) // claims ledger game 1
duel, err := mm.Queue(arenaDuel(g))                                  // your queues, at startup
// after admin.Register: partyadmin.Register(adm, parties); matchmakingadmin.Register(adm, mm)
```

`Service.Queue` validates a `QueueConfig`, fills the defaults and rejects a duplicate name. Keep
the `*Queue` for `Create`.

**A ranked 1v1** (no money; the rating key defaults to the queue name):

```go
matchmaking.QueueConfig{
	Name: "duel", Teams: []matchmaking.Team{{Min: 1, Max: 1}, {Min: 1, Max: 1}}, Ranked: true,
	OnMatch: s.startMatch, OnFinish: s.matchDone,
}
```

A queue needs two teams, or a free-for-all: `Teams: []matchmaking.Team{{Min: 1, Max: 1}}, FFA: &matchmaking.Team{Min: 2, Max: 8}`.

**A staked 5v5 with parties** (a party of 1..5 queues together and plays on one team):

```go
matchmaking.QueueConfig{
	Name: "arena5", Teams: []matchmaking.Team{{Min: 1, Max: 5}, {Min: 1, Max: 5}}, Ranked: true,
	Game:   pvp, // *gameconfig.Game from a.Games.Game(...): Check()s every stake against its limits
	Stakes: map[ledger.Currency][]int64{"usd": {100, 500, 2_000}},
	FeeBP:  500, // 5%; the default is ledger.FeeBP, NoFee: true for none
	OnMatch: s.startMatch, OnFinish: s.matchDone,
}
```

### Player flow (RPCs and pushes)

- `party.create {max, name, open}`, `party.join {party_id}`, `party.accept {party_id, user_id}`
  (a leader approves a request to a closed party), `party.leave`, `party.kick`, `party.lead`
  (hand over leadership), `party.get`, `party.send {party_id, kind, data}`. Errors 1020-1026. The
  party pushes its changes to every member. `party.get {party_id}` answers anyone who has the ID:
  the ID is the invite, so share it only with whom you'd let see the members.
- `match.queue {queue, currency, stake, properties, numbers, idem_key}` returns one of
  `{ticket}`, `{match_id}`, `{removed}`. A party leader queues the whole party; a member gets
  `party.ErrNotLeader`. A repeat of the `idem_key` returns the stored outcome.
- `match.cancel {}` leaves the queue; `match.status {}` returns `{ticket, matches}` (what a
  reconnecting client needs); `rating.get {key}`.
- Pushes to the user channel, by `type`: `match.ticket`, `match.ticket_removed` (`reason`:
  `cancelled`, `party_changed`, `expired`, `offline`, `insufficient_funds`, `already_queued`: a
  re-queue after a cancelled match found the user already queued again), `match.found` (`match`),
  `match.finished` (`match`, `payouts`, `ratings`), `match.cancelled` (`match`, `reason`).
- Errors 1030-1036: `ErrQueued`, `ErrBusy`, `ErrUnknownQueue`, `ErrPartyTooBig`, `ErrNotFound`,
  `ErrResultConflict`, `ErrClosed`.

### The game's side of a match

```go
func (s *Service) startMatch(ctx context.Context, m matchmaking.Match) error {
	// m.Players: UserID, Team, PartyID; m.Ref names the match. Stakes are held already.
	return s.createRoom(ctx, m.ID) // INSERT ... ON CONFLICT (match_id) DO NOTHING
}
```

- **`OnMatch`** runs once the stakes are held, while the match is `forming`. Return an error and
  the match is cancelled and refunded (reason `on_match`). It can run twice for one match (a crash,
  two instances), so make it **idempotent**.
- **`Finish(ctx, matchID, matchmaking.Result{Ranks: …})`** reports the result: one rank per team,
  1 is best, ties share a rank, a forfeiting team simply ranks last. The same result again is a
  no-op; a different one is `ErrResultConflict`; a cancelled match is `ErrClosed`. It pays the pot,
  updates ratings, pushes `match.finished`, then calls `OnFinish`. If the ledger is down it returns
  the error and the reconciler finishes the settlement from the stored result.
- **`Cancel(ctx, matchID, reason)`** refunds every stake and pushes `match.cancelled` (a player left
  before the start, the server crashed). A match whose result is in (`finishing` or `settled`)
  returns `ErrClosed`: the result stands.
- **`MatchTimeout`** (default 1 h) is the safety net: a match still forming or running past it is cancelled
  (reason `timeout`) and refunded. Set it above your longest game.
- **`OnFinish`** runs after settlement, outside any transaction, so it may `Finish`, `Cancel` or
  `Create` other matches (a tournament's next round). It is **at least once**: an error or a crash
  makes the reconciler call it again, so make it idempotent.
- **`Payout(pot, m, result) []ledger.PotWin`** replaces the default (the best rank's players split
  the pot less the fee equally). It must pay players of the match exactly `pot - ledger.FeeOf(pot, m.FeeBP)`, at most
  `ledger.MaxPotWinners` winners; it must be deterministic. Its answer is stored when `Finish`
  records the result, and settling never calls it again; a wrong answer makes `Finish` return an error
  and store nothing.

### Direct matches

A tournament round or a challenge has no queue wait: `Create` runs the same pipeline (stakes held,
`OnMatch`, `match.found`) for teams you name. Players need not be online.

```go
m, err := mm.Create(ctx, matchmaking.NewMatch{
	Queue: duel, Teams: [][]int64{{alice}, {bob}}, Currency: "usd", Stake: 500,
	Ref: fmt.Sprintf("round:%d:%d", tournamentID, n), // idempotent; never "ticket:…"
})
```

A repeat of a `Ref` returns the existing match whatever its state. `ErrBusy` means a player is in an
open match of a queue that isn't `Concurrent`; `ledger.ErrInsufficientFunds` comes back with the
cancelled, refunded match.

### Policy knobs

All in `QueueConfig`, all with defaults:

- `AllowOffline`: queue and match players who are not connected (default: all must be online).
- `Concurrent`: a player may be in several open matches of the queue (default: one).
- `FillAfter` (10 s): after this wait a team may fill below its `Max`, down to `Min`.
- `MaxWait` (2 min): a ticket older than this is removed (`expired`); `matchmaking.Forever` keeps it.
- `RatingWindow`/`RatingWiden`: rating gap allowed, and how much it grows per 10 s of waiting.
- `DrawRefund`: a result where every team ties refunds the stakes instead of splitting the pot.
- `NoFee`: a real 0% fee (`FeeBP: 0` means the default).
- `Properties`: client-sent strings that must be equal (`{"region"}`); `Numbers`: client-sent
  numbers that may differ by `Diff`, plus `Widen` per `Every` of waiting.
- `Prepare(ctx, *Ticket)`: set `t.Server[...]` from your own data (a league tier); equal `Server`
  maps are required to match. The client can't send these and never sees them (`Ticket.Server` is
  not in the JSON). Prepare gets a copy of the ticket: only `Server` is kept.
- `Compatible(a, b Ticket) bool`: a last veto on any two tickets in a match.

## Checklist

- [ ] One game ID per table of operation IDs, in `FirstBrandGame..MaxGame`, registered with
      `RegisterGame`, never changed
- [ ] Own migrations and goose table; sqlc for every query
- [ ] `UNIQUE (user_id, idempotency_key)`; retries return the same row
- [ ] Row before money; money moved by row ID; reconciler registered
- [ ] No balance stored or checked in the game
- [ ] Integer minor units; `math/big` for multipliers, rounded down
- [ ] Chance games: `fair.Next` in the row's transaction, seed pair and nonce on the row, no
      `math/rand`, guard for multi-step games
- [ ] User ID from the connection, never the payload
- [ ] Tested through its RPCs with `apptest`
- [ ] Admin view, if any, in a `<game>admin` subpackage; actions change rows, never money
- [ ] Multiplayer: `OnMatch` and `OnFinish` idempotent (both can run again); `Payout` deterministic
      and summing to pot less fee; `MatchTimeout` above the longest game; `Create` Refs unique per
      match; every match ends in `Finish` or `Cancel`
