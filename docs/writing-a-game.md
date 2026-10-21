# Writing a game

A game is a Go package in your brand's repo, next to your `main`. It owns its tables, registers its
RPCs on the app, and moves money only through `*ledger.Ledger`. `games/dice` is the worked
example; read it alongside this guide.

```go
// your main, after app.New
fair, err := fairness.Register(ctx, a, sealer) // only if any game draws random outcomes
_, err = crash.Register(ctx, a, fair)
```

## 1. Pick a game ID, once, forever

```go
const Game ledger.Game = ledger.FirstBrandGame + 1 // your games: FirstBrandGame..MaxGame
```

The ID is part of every transfer ID your game creates. Changing it, or reusing another game's ID,
makes retries and reconciliation move money twice. IDs below `ledger.FirstBrandGame` belong to the
platform's games. Call `a.Ledger.RegisterGame(Game, "crash")` first thing in `Register`: a
duplicate ID in the deployment then fails at startup. The ledger refuses IDs outside
`1..ledger.MaxGame`.

## 2. Own your tables

- Migrations in `migrations/*.sql`, embedded, run with `a.Migrate(ctx, migrations, "goose_<game>")`.
  After your first release, never edit a migration: add a new one.
- Queries with sqlc. List the platform's schemas your tables reference in `sqlc.yaml` (`users` is
  in gamify's `db/migrations`, `seed_pairs` in `fairness/migrations`).
- A bet row holds metadata only: amount, currency, outcome, payout, status. The ledger holds the
  balances. Never store or check a balance yourself; the ledger refuses overdrafts
  (`ledger.ErrInsufficientFunds`).
- Money is `int64` minor units. Never a float. Multiplier math with `math/big`, rounded down.

## 3. Every bet is idempotent

The client sends an idempotency key with every bet (`idem_key`, at most 64 bytes). Put
`UNIQUE (user_id, idempotency_key)` on the bet table and insert with `ON CONFLICT DO NOTHING`; on
conflict, return the bet that's already there. A retried request must return the same bet and
never charge twice.

The user ID comes from the RPC handler's `userID` argument (the authenticated connection), never
from the payload.

## 4. Record first, then move money

Dice's order, which every game follows:

1. **Create** the bet in one transaction: draw the outcome (`fair.Next(ctx, tx, userID, n)`, see
   below), insert the bet row as `pending`. Commit.
2. **Settle**: call the ledger with the bet's row ID. Transfer IDs are derived from
   `(game, leg, bet ID)`, so calling it again for the same bet is a no-op.
   - `ledger.ErrInsufficientFunds`: mark the bet `voided`.
   - Any other error: leave it `pending` and return the error.
   - Success: mark it `settled`.
3. **Reconcile**: register `a.Reconcile(s.Reconcile)`. Every 15 s it settles bets still `pending`
   after a crash or a ledger outage, through the same settle function.

Multi-step games (Mines, Blackjack) hold the stake first (`HoldStake`, `HoldExtra`) and post or
void it at the end (`SettleHeld`, `SettleHolds`). PvP games use pots (`HoldPot`, `SettlePot`,
`VoidPot`, `SettlePool`).

## 5. Games of chance: provable fairness

- Take the `*fairness.Service` that `fairness.Register` returns. Draw with
  `fair.Next(ctx, tx, userID, n)` **in the same transaction as the bet row**: the nonce increment
  and the bet commit or roll back together.
- Store the draw's `SeedPairID` and `Nonce` on the bet (`UNIQUE (seed_pair_id, nonce)`), so the
  player can verify it after `seed.rotate`.
- Turn the draw into an outcome only with `fairness` functions (`SlideWindow`, `Floats`). No
  `math/rand` anywhere in a game. Publish your outcome formula: players verify with it.
- Never log or return an unrevealed server seed.
- A multi-step game registers `fair.Guard(...)` so the seed can't rotate (and reveal the outcome)
  while a game is live.

## Bet history

Register `a.Bets("crash", s.History)` so the admin can list your bets. The `app.BetSource` lists
newest first (`beforeID` keyset, `userID` 0 = every user) and fills `app.Bet`: the row's amount,
payout, status and a `Detail` JSON with your game's own fields. `Detail` never holds the server
seed, nor outcomes the player hasn't seen yet (e.g. a live round's future steps). See
`games/dice/service.go`.

## Admin view

A game can add its own page to the admin console, described in Go; the console's UI draws it. Put
it in a `<game>admin` subpackage (`crash/crashadmin`) that imports `admin` and your game, so the
game itself never imports `admin` and brands without the console don't pull it in. Export from the
game only what the view needs (Dice: `Service.ListBets` and `BetRow`). The brand calls
`crashadmin.Register(adm, s)` after `admin.Register` and before `a.Run`.

```go
return adm.View(admin.View{
	Name: "crash-rounds", Title: "Crash rounds", Group: "Crash", Perm: admin.PermView,
	UserFilter: "user_id", // also a tab on the player page
	Columns: []admin.Column{
		{Key: "id", Type: admin.Int},
		{Key: "user_id", Type: admin.UserID},
		{Key: "pot", Type: admin.Money, CurrencyKey: "currency"},
		{Key: "status", Type: admin.Enum, Options: []string{"open", "settled", "voided"}},
	},
	Filters: []admin.Filter{{Key: "user_id", Type: admin.UserID}},
	List:    func(ctx context.Context, q admin.Query) (admin.Page, error) { … }, // keyset: q.Before, q.Limit
	Actions: []admin.Action{{Name: "void", Title: "Void round", Perm: "crash.void", Reason: true,
		Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (before, after any, err error) { … }}},
})
```

- Field types: `Text`, `Int`, `Decimal` (an integer shown with `Scale` decimals, never a float),
  `Money` (minor units; currency from the row's `CurrencyKey` or a fixed `Currency`), `Bool`,
  `Time`, `Enum` (`Options`), `UserID` (links to the player), `JSON`. Filters are columns of the
  same type; action inputs (`Fields`) use the same types, `Money`/`Decimal` as integers.
- `View` checks the description and returns an error; register views at startup.
- Actions never move money: the ledger isn't in `Run`'s transaction. Change your game's row (mark
  the round void) and let your reconciler do the ledger part, as for a bet left by a crash. The
  console writes the audit row in the same transaction; an error or a panic rolls both back.
- Permissions: a built-in (`admin.PermView`, …) or one of the brand's in
  `admin.Config.Perms` (`"crash.void": {admin.RoleOperator}`); owners hold them all. Name yours
  `<game>.<verb>` so they can't clash with a built-in.
- Worked example: `games/dice/diceadmin`.

## 6. Limits and errors

- Declare your limits with `a.Games.Game("crash", maxBet, maxPayout, variants)`; admins can lower
  them. Check every new bet with `Stake` before creating it; reject a bet that could pay more than
  the limit, never cap it silently.
- Client errors are `*realtime.ClientError` with codes from 1100 up; reuse
  `gameconfig.ErrInvalid` (1100) for bad requests. Any other error reaches the client as a generic
  internal error.

## 7. Test it through its RPCs

`apptest` runs a real App in a test: a fresh Postgres database per test (dropped afterwards), the
Postgres ledger and an in-memory transport instead of Centrifugo. Set `TEST_DATABASE_URL` to any
database on a Postgres server you can create databases on; tests skip without it.

```go
func TestCrash(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	fair, _ := fairness.Register(ctx, a.App, apptest.Sealer(t))
	if _, err := crash.Register(ctx, a.App, fair); err != nil {
		t.Fatal(err)
	}
	alice := a.User("alice")
	a.Fund(alice, "usd", 1_000)
	res, cerr := apptest.Call[crash.BetResult](a, alice, "crash.bet", crash.BetRequest{…})
	// a.Balance, a.Pushes(realtime.UserChannel(alice)), a.ReconcileNow()
}
```

Test at least: a bet without money (`ErrInsufficientFunds` path), a winning and a losing bet
against `a.Balance`, a retry with the same idempotency key charging once, a bet left pending
finished by `a.ReconcileNow()`, and (games of chance) the revealed seed reproducing the outcome.
`games/dice/apptest_test.go` is the worked example.

## Checklist

- [ ] Game ID in `FirstBrandGame..MaxGame`, registered with `RegisterGame`, never changed
- [ ] Own migrations and goose table; sqlc for every query
- [ ] `UNIQUE (user_id, idempotency_key)`; retries return the same bet
- [ ] Bet row before money; settle by bet ID; reconciler registered
- [ ] No balance stored or checked in the game
- [ ] Integer minor units; `math/big` for multipliers, rounded down
- [ ] Chance games: `fair.Next` in the bet's transaction, seed pair and nonce on the bet, no
      `math/rand`, guard for multi-step games
- [ ] User ID from the connection, never the payload
- [ ] Tested through its RPCs with `apptest`
- [ ] Admin view, if any, in a `<game>admin` subpackage; actions change rows, never money
