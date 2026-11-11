# gamify v1.2: payments (deposits and withdrawals)

Nov 11, 2026

> **Release (owner, Nov 11, 2026):** payments is v1.2.0. The multiplayer releases move down one:
> leaderboards v1.3, tournaments v1.4, leagues v1.5, cross-feature docs v1.6. See
> `docs/superpowers/plans/2026-10-22-v1.1-index.md`. Supersedes §3 of `gamify platform changes.md`.

> **Implementation notes, Nov 11, 2026:** where the build differs from the text below.
> - A new `holding` status: a withdrawal row leaves it only after the ledger hold is confirmed.
>   §5's "requested rows without their ledger hold" means `holding`.
> - `DepositLimits` and `WithdrawLimits` have an `Off` switch (stops new requests).
> - Deposit address targets are per (method, user); amount and currency checks apply only to invoices.
> - `StateUnknown`: `Sender.Status` answers it only when it never saw the ID; only then is the payout re-sent.
> - The fee is taken from the requested amount: payout = amount - fee, fee goes to `house`.
> - `Resolve` needs an expired send lease (`ErrSendInProgress` otherwise).
> - `ErrWrongStatus` is a plain Go error, not a client error.

## Intent

Some currencies are real money (USDT in a casino): users deposit them from outside and withdraw
them back out (to a wallet address, a bank, any rail). Others (coins, tickets) never leave the
platform. gamify moves the money inside the ledger; the brand brings the code that talks to the
outside world, as a `Receiver` (money in) and a `Sender` (money out) per method, registered in Go.

Out of scope: refunds and chargebacks (card / Telegram Stars). Crypto has none; decide before the
first card or Stars provider (debit what's left, record the rest, flag the user). The design keeps
room for it: a negative event on a deposit row, no change to the interfaces below.

## 1. Currency switch

`ledger.CurrencyConfig.Withdrawable bool`, off by default; `CURRENCIES` gets a `w` flag
(`usdt:3:1:0:6:w`; flags `t`/`w` combine as `tw`). The ledger refuses `Withdraw` in a currency
without it, so misuse fails at the money layer, not only in `payments`. Deposits need no flag:
a currency takes deposits when a `Receiver` is registered for it.

## 2. Ledger

- New system account `outflow` per currency (`kindOutflow`, account code `codeOutflow`), the mirror
  of `inflow`. Created by the usual idempotent currency setup on start; shows in `SystemBalances`
  and the treasury page.
- New transfer code `codeWithdraw`, namespace `nsWithdraw = 10`, legs hold / post / void, keyed by
  the withdrawal row ID.
- `Withdraw(ctx, c, withdrawalID, userID, amount)`: pending transfer user → outflow. The money stops
  being spendable at once. Respects the welcome floor (same check as `HoldPot`), so a play-only
  grant can't be cashed out. Overdraft refused by the driver.
- `PostWithdraw(ctx, c, withdrawalID)`: the money left (paid).
- `VoidWithdraw(ctx, c, withdrawalID)`: the money goes back (rejected, cancelled, failed).
- Deposits keep the existing `ledger.Deposit` (`nsDeposit`), keyed by the deposit row ID.
- `ledgertest` cases for all three on Postgres and TigerBeetle: hold, post, void, double post/void,
  non-withdrawable currency, welcome floor, overdraft.

## 3. Brand interfaces

```go
// payments
type Receiver interface {
	Name() string // the method: "trc20", "bep20", "stars", ...
	// Deposit tells the user how to pay: a fixed address (crypto) or an invoice / checkout link.
	Deposit(ctx context.Context, req DepositRequest) (DepositInfo, error)
	// Run watches for payments until ctx ends (chain poller, webhook routes on h.Mux, bot updates)
	// and reports each confirmed one with h.Received.
	Run(ctx context.Context, h *Host) error
}

type Sender interface {
	Name() string
	// ValidateDestination rejects a bad destination before any money is held.
	ValidateDestination(dest string) error
	// Send pays out. p.ID is the idempotency key: a second Send with the same ID must not pay twice.
	Send(ctx context.Context, p Payout) (Result, error)
	// Status reports what happened to a payout; the reconciler asks after a crash or an unknown result.
	Status(ctx context.Context, payoutID int64) (Result, error)
}

type Result struct {
	State  State  // Pending, Paid, Failed; Unknown (Status only: never saw this ID, safe to Send again)
	Ref    string // tx hash, provider ID
	Reason string
}
```

- `Host` is concrete (`Mux`, `Received`), not an interface. One `Run` shape covers webhooks, chain
  polling and Telegram bot updates (Stars arrive as bot updates, which a `Webhook(*http.Request)`
  method couldn't take).
- Amounts cross the interface in the currency's minor units; the receiver/sender converts from the
  chain's decimals.
- Interfaces are justified (CLAUDE.md): every brand writes its own implementations.

Wiring:

```go
pay, err := payments.Register(ctx, a, payments.Config{})
pay.Receiver("usdt", tron.NewReceiver(...)) // several methods per currency are fine
pay.Receiver("usdt", bsc.NewReceiver(...))
pay.Sender("usdt", tron.NewSender(...))     // fails at startup if usdt isn't Withdrawable
```

## 4. Deposits

1. `payments.deposit {currency, method}` → `Receiver.Deposit`. Crypto addresses are stored per
   (method, user) and reused; invoices are stored with their amount.
2. The receiver reports `Received({ExternalID, Address | InvoiceID, Currency, Amount})`.
3. Insert the deposit row `ON CONFLICT (method, external_id) DO NOTHING`, read it back, resolve the
   user from the stored address or invoice, credit `ledger.Deposit(rowID)`. A redelivered event
   credits once; an unknown address or invoice credits nothing and is recorded as `unmatched`.
4. Invoice deposits must match the stored amount and currency or nothing is credited. Address
   deposits: the receiver is the verifier and reports only confirmed on-chain amounts (its own
   confirmation count).
5. Below the per-method minimum: recorded `ignored`, not credited (admin can credit by adjustment).

States: `credited`, `ignored`, `unmatched`.

## 5. Withdrawals

```
requested --(amount ≤ auto-approve | admin approve)--> approved --Send--> sending --> paid    (post)
    └─> rejected / cancelled (void)                                          └─> failed  (void)
```

- `payments.withdraw {currency, method, amount, destination, idempotency_key}`: validate config
  limits and `ValidateDestination`, insert the row (unique `(user_id, idempotency_key)`), then
  `ledger.Withdraw(rowID)` in that order, so a retry or the reconciler never holds twice.
- Fee: config per method, recorded on the row and posted with the withdrawal (to `house`).
- `payments.cancel` only while `requested`.
- **Money safety rule:** a `Send` error or unknown result never voids. The row stays `sending`; the
  reconciler asks `Status(id)` (and re-sends with the same ID only if the sender says it never saw
  it). Only a definite `Failed` voids. A timeout must not end in paid on-chain *and* refunded.
- Reconciler: `requested` rows without their ledger hold, `approved` rows not yet sent, `sending`
  rows, through `a.Reconcile`.

## 6. Config (no imposed policy)

Per currency × method, Go defaults, editable in the console: enabled, min/max amount, daily cap per
user, fee, auto-approve threshold (default 0: every withdrawal needs a human), minimum deposit.

## 7. Reads

- Player RPCs (user from the connection only): `payments.deposit`, `payments.withdraw`,
  `payments.cancel`, `payments.history` (own deposits and withdrawals, filter by kind/status), push
  `payments.updated` on every state change. Activity history via `a.History`.
- Go API for a brand's backend: `pay.Withdrawals(ctx, userID, filter)`, `pay.Deposits(...)`, `Get`,
  `Approve`, `Reject`.

## 8. Admin

- Deposits and Withdrawals pages (list tools: status, method, currency, time range, search, CSV).
- Withdrawal detail: destination, tx link, ledger legs, the user's deposits vs. withdrawals and
  recent wins. Approve/reject: new permission, typed confirmation, audited; above a configured
  amount a second console user must also approve (as adjustments' `ApproveAbove`).
- Methods page: each receiver/sender's health, per-method config.
- Player page: payments tab. Treasury: `inflow` and `outflow` per currency.

## 9. Upgrade notes

Additive: new module and tables (`goose_payments`), new ledger account and namespace, new
`CurrencyConfig` field. Existing currencies stay non-withdrawable until the brand sets the flag.
