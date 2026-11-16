# gamify: feature flags

Nov 16, 2026. Target: the next minor release (the number is the owner's call).

## Intent

Operators want to change what players see without a new build of the game: show the new lobby to
10% of players, widen it to everyone, hide a button, switch a section off at once when it breaks.
The brand's developer writes `if flag("new_lobby")` in the game once; from then on the operator
alone decides, in the admin console, who sees it.

Flags are created **entirely from the console**. Nothing is declared in Go or in the brand's
config: the game code names a key and a fallback, the operator creates the key and its rollout.

## Goals

- Operators create, edit and delete on/off flags in the console, with no deploy.
- Gradual rollout by percentage: 10% → 50% → 100% only ever **adds** players (whoever had it keeps
  it).
- Per-flag testers: specific players who see the flag whenever it is on, even at 0%.
- An off switch that turns the flag off for everyone, testers included, instantly in the console.
- The game client reads the caller's flags with one RPC; Go code on the server can read them too.
- Every change is in the audit log.

## Non-goals (v1)

Each can be added later without a breaking change; the design notes where.

- Values other than on/off (strings, numbers, JSON, A/B variants). A `type` column exists from day
  one with `bool` the only allowed value.
- Pushing changes to players already connected. Clients pick up changes on their next `flags.get`.
- Targeting by player attributes (country, login provider, segments).
- Per-game scope. Flags are brand-wide; a key may carry a game prefix by convention
  (`crash.new_lobby`).
- A report of keys clients ask for that no flag has.
- Flags for players who are not logged in (the RPC needs a connection, which needs a login).

## Anti-goals

- Over-engineering: no rules engine, no SDK, no separate service.
- A flag must never touch money, limits or game rules the platform enforces. Bet caps and turning a
  game off stay in `gameconfig`; flags only tell the brand's own code what to show.
- A missing, deleted or not-yet-loaded flag must never break a game: every read has a fallback.

## Constraints (from CLAUDE.md)

- Library first: a new opt-in module `flags` with `flags.Register`, plus `flags/flagsadmin` for the
  console, like `restrict` / `restrictadmin`.
- The module owns its tables and migrations (goose table `goose_flags`, via `a.Migrate`).
- sqlc for every query; generated code in `flags/internal/store`.
- Identity of the player comes only from the connection, never from the RPC payload.
- No new interface (one implementation).
- `CHANGELOG.md` under Unreleased with the migration noted.
- Go is built test-first; the console UI is not (memory: no TDD for UI).

## 1. Data

```sql
-- +goose Up
CREATE TABLE flags (
    id          bigserial   PRIMARY KEY,           -- the console row ID (admin.View needs int64)
    key         text        NOT NULL UNIQUE,       -- immutable once created
    description text        NOT NULL DEFAULT '',
    type        text        NOT NULL DEFAULT 'bool' CHECK (type IN ('bool')),
    enabled     boolean     NOT NULL DEFAULT false,
    percent     smallint    NOT NULL DEFAULT 0 CHECK (percent BETWEEN 0 AND 100),
    version     bigint      NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE flag_testers (
    flag_id  bigint NOT NULL REFERENCES flags(id) ON DELETE CASCADE,
    user_id  bigint NOT NULL REFERENCES users(id),
    PRIMARY KEY (flag_id, user_id)
);
```

- **Why an `id` (my call, from the builder check):** `admin.View` rows, actions and audit
  targets use int64 IDs (`admin/view.go`, `view_http.go`). A list-position ID like `pay-methods`
  uses is unsafe because flags are created and deleted at runtime, and widening `admin.View` to
  string IDs is a platform change out of scope here. The audit target is `flags:<id>`, so a
  deleted-and-recreated key gets a new ID. Before and after always include `key`, so history
  stays searchable by key.

- **Key format:** `^[a-z0-9][a-z0-9_.-]{0,63}$`, checked in Go on create. A key can't be renamed
  (clients hold it); rename = delete + create.
- **Later types:** widening the `CHECK` and adding a `value jsonb` column is a new, additive
  migration. Existing bool rows keep working.

## 2. Evaluation

For player `u` and flag `f`, in this order:

1. No flag with that key → the caller's fallback.
2. `f.enabled` is false → `false` (the off switch beats testers).
3. `u` is in `f`'s testers → `true`.
4. `bucket(f.key, u) < f.percent * 100` → `true`, else `false`.

`bucket(key, userID)` = the first 8 bytes of `SHA-256(key + ":" + decimal(userID))` as a big-endian
uint64, mod 10000. It lands in 0..9999, which leaves room for steps finer than 1% later without
reshuffling anyone.

- **Same answer every time:** a player's bucket never changes, so a higher percent is a superset
  of a lower one. Because the key is in the hash, each flag picks a different 10%.
- **The hash is frozen.** Changing it would reshuffle every rollout in production. Pin it with test
  vectors in `flags_test.go` (key, user ID → bucket), the same way `fairness` pins its vectors.
  This is not randomness, so the no-`math/rand` rule is not involved.

## 3. Go API (`flags`)

```go
type Config struct {
    // ReloadEvery is how soon a change made on another instance applies here (default 5s).
    ReloadEvery time.Duration
}

func Register(ctx context.Context, a *app.App, cfg Config) (*Service, error)

// Bool is the flag's value for this player; fallback when no flag has the key.
func (s *Service) Bool(userID int64, key string, fallback bool) bool

// For is every flag's value for this player (what flags.get returns).
func (s *Service) For(userID int64) map[string]any

// Console-facing (take the admin's tx, return before/after for the audit row, like restrict.Set):
type Flag struct {
    ID               int64
    Key, Description string
    Type             string // "bool"
    Enabled          bool
    Percent          int
    Testers          []int64
    Version          int64
    UpdatedAt        time.Time
}
func (s *Service) List(ctx context.Context) ([]Flag, error)
func (s *Service) Get(ctx context.Context, id int64) (Flag, error)
func (s *Service) Create(ctx context.Context, tx pgx.Tx, f Flag) (Flag, error)
func (s *Service) Update(ctx context.Context, tx pgx.Tx, f Flag) (before, after Flag, err error) // by f.ID: description, enabled, percent
func (s *Service) SetTesters(ctx context.Context, tx pgx.Tx, id int64, userIDs []int64) (before, after Flag, err error)
func (s *Service) Delete(ctx context.Context, tx pgx.Tx, id int64) (before Flag, err error)
func (s *Service) Applied(ctx context.Context) // reload this instance at once after a console write
```

- Errors: `ErrBadKey`, `ErrExists`, `ErrNotFound`, `ErrBadPercent`, `ErrUnknownPlayer` (a tester
  that is not a user).
- `Register` runs the migration, loads every flag and its testers into memory, starts
  `a.Loop(ReloadEvery, reload)` and handles the RPC. Reads never touch Postgres. A failed reload
  logs and keeps the last snapshot.
- `map[string]any` keeps `For` (and the RPC) open to other value types later. `Bool` gains siblings
  (`String`, `Int`) as new functions.

## 4. RPC

`flags.get` with no payload returns the caller's flags:

```json
{"flags": {"new_lobby": true, "shop_button": false}}
```

- The user is taken from the connection only.
- Every flag is listed, off ones as `false`. Descriptions and testers are not returned.
- Keys are visible to any logged-in player. The README says so: don't put secrets in a key.
- The client calls it at start (and may call it again, e.g. on resume). Old clients only read the
  keys they know, so adding flags or value types never breaks them.

## 5. Console (`flags/flagsadmin`)

One view, **Flags**, using the existing `admin.View` machinery, like `restrictadmin`:

- **Columns:** key, description, enabled, percent, tester count, updated at.
- **Page action "New flag":** key, description, enabled (default off), percent (default 0).
- **Row actions:**
  - "Edit": description, enabled, percent.
  - "Testers": a list of player IDs; replaces the list.
  - "Delete": with a warning that clients fall back to their default.
- **Audit:** each action is audited with before and after through the existing action audit.
- **Permission:** a new `admin.PermFlags = "flags.edit"`, given to owner and operator by default.
  Viewing needs `PermView`. Brands' custom roles need it added (upgrade note).
- **Testers on the player page:** a "Flags" tab listing the flags the player tests is a nice-to-have.
  Leave it to the builder; drop it if it isn't cheap with the existing `UserFilter`.

## 6. Wiring and docs

- `cmd/gamify` registers `flags` and, when the console is on, `flagsadmin`.
- README:
  - Add flags to the feature list.
  - Add a short "Feature flags" section: how to read them in a client, the fallback rule, that
    keys are public, that rollout is sticky.
- `docs/writing-a-game.md`: a short client example (`flags.get` at start, read with a default).
- `CHANGELOG.md` Unreleased:
  - Added: `flags`, `flagsadmin`, RPC `flags.get`, `admin.PermFlags`.
  - Upgrade notes: migration `goose_flags`; custom roles need `flags.edit` to edit flags.

## 7. Tests (Go, test-first)

- **Evaluation table:**
  - unknown key → fallback
  - off → false (tester too)
  - on at 0% → testers only
  - on at 100% → everyone
- **Pinned bucket vectors** (frozen).
- **Rollout only adds players:** for many users, the set at p% ⊆ the set at q% when p < q, and
  roughly p% of users land in it.
- **Create and update errors:** bad key, duplicate key, percent out of range, tester that isn't a
  user, update or delete of an unknown key.
- **Reload:** a change written on one `Service` shows up on another after reload. `Applied`
  updates its own instance at once.
- **RPC:** `flags.get` returns the caller's own values (a tester and a non-tester get different
  answers for the same flag).
- **flagsadmin:** each action writes an audit row with before and after; edits need `flags.edit`.

## Left to the builder

- Exact query shapes, file layout inside `flags/`, console labels and help text.
- Whether "Testers" is its own action or part of "Edit".
- The player-page tab (§5).
