# gamify: white-label game platform

Library first: brands import the packages and wire them in their own `main` (`cmd/gamify` is the
sample). See README.md for the layout, drivers and commands.

## Rules

- Money: integer minor units (`int64`), never floats; multiplier math with `math/big`, rounded down.
  The ledger driver is the source of truth for balances. Game tables hold game metadata
  only. No app-side balance checks: drivers refuse overdrafts.
- Ledger drivers implement TigerBeetle's semantics exactly. `ledger/ledgertest` is the contract: a
  driver change must pass it on Postgres *and* TigerBeetle. Never weaken a suite case to make a
  driver pass. Match TigerBeetle instead.
- Transfer IDs are deterministic (`ledger.betTransferID`, namespaces in `ledger.go`). Never random
  for anything a retry can repeat. Never reuse or change a `ledger.Game` constant or a currency's `Ledger`.
  Platform games use IDs below `ledger.FirstBrandGame`; brands' games `FirstBrandGame..MaxGame`.
- Interfaces only where a second implementation exists or is the point (`ledger.Driver`,
  `realtime.Transport`). Everything else is concrete.
- A module owns its tables and migrations (own goose table via `db.Migrate`); others call its Go API.
- Semver, brands import us: once a version is tagged, never edit a committed migration (add a new
  one) and never break an exported API outside a major version (`/v2` module path). Generated code
  lives in `internal/store` so it's never public API. Every user-visible change goes in
  `CHANGELOG.md` under Unreleased, with upgrade notes for migrations and API changes.
- sqlc for every query (no SQL strings in Go). Generated `store/` dirs: don't edit, run `sqlc generate`.
- User identity comes only from the transport's authenticated connection, never from an RPC payload.
- Every money-moving RPC carries an idempotency key.
- Provably fair: never change a formula or the pinned vectors in `fairness/fairness_test.go` (they
  match the public verifier). Never log or return an unrevealed
  server seed. Nonce increment happens in the same transaction as the game's row (`fairness.Service.Next`).
  No `math/rand` in fairness or games.
- macOS: `CGO_LDFLAGS=-Wl,-ld_classic`.
