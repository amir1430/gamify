# Changelog

All notable changes, for brands upgrading. Format: [Keep a Changelog](https://keepachangelog.com),
versions: [semver](https://semver.org). Upgrade notes call out migrations and API changes.

## Unreleased

## [1.1.6] - 2026-10-31

### Fixed
- `admin` UI: brand views (`Admin.View`) were served by the API but never drawn, so no brand page
  (and no restrictions editor) could be opened. The console now builds them from `GET /api/views`:
  a sidebar section per `Group` between Operations and Admin, a page per view at `/views/<name>`
  (typed columns, filters in the URL, keyset "Load more", a detail dialog when the view has `Get`),
  actions as forms (fields by type, prefilled from the row's columns of the same key; the reason
  when `Reason`; the `Confirm` text), and a player-page tab for each view with a `UserFilter`
  (e.g. `restrictadmin`'s Restrictions). No API or Go change; rebuild nothing, the embedded
  `admin/ui/dist` carries it.

## [1.1.5] - 2026-10-31

### Added
- `config`: optional RPC routing by namespace, for brands that deploy parts apart (e.g. single-instance
  game rooms next to scaled app instances). `centrifugo.rpc_namespaces` lists every RPC namespace the
  app serves (a method's part before `.`, the platform's included); `centrifugo.proxies`
  (`name`, `endpoint`, `namespaces`) sends some of them to another endpoint, with `proxy_timeout`.
  `gamify-config` then writes `rpc.namespace_boundary: "."`, `rpc.namespaces` and `proxies`.
  Unset (the default) is unchanged: every RPC goes to `proxy_endpoint`. Centrifugo refuses a method
  whose namespace isn't listed, so keep the list complete (a brand test over `Router.Methods()`).

## [1.1.4] - 2026-10-31

### Fixed
- `fairness`: `Service.Next` creates a player's missing seed pairs in the bet's own transaction.
  It took a second pool connection while holding the transaction's, so as many first bets at once
  as the pool has connections (players without pairs, e.g. a launch spike) deadlocked every bet
  until the requests timed out. Regression test `TestFirstBetsDontStarvePool`. No API change.

## [1.1.3] - 2026-10-31

### Added
- `app`: `App.OnLogin(f)`, a hook every successful login runs (after `LoginCheck`, before the token
  is sent) with `auth.Login` (`Created` on a first login) and the verified `auth.Identity` (its
  `Meta`, e.g. Telegram's `start_param`): referral attribution, welcome flows, analytics. Its error
  is logged; the login goes on.

## [1.1.2] - 2026-10-31

### Added
- `restrict`: new opt-in module for per-player restrictions set by operators.
  `restrict.Register(ctx, app, restrict.Config{Blocks: …})` adds the `user_restrictions` table
  (goose table `goose_restrict`), the login check, the RPC check and a reload loop
  (`Config.ReloadEvery`, default 5 s). A suspended player can't log in (403
  `{"error":"this account is suspended"}`) and every RPC is refused (1108); a block refuses the RPC
  methods of one group the brand names in `Config.Blocks` (1108), so the player can still finish
  what is in play. Each lasts until lifted or until a time. `Service.Set` writes in the caller's
  transaction, `Applied` makes it take effect at once and drops a suspended player's connections.
- `restrict/restrictadmin`: the console's Restrictions page (also a tab on the player page) with
  "Set restriction" and "Lift" actions, behind the new permission `restrict.set`
  (`admin.PermRestrict`: owner, operator, support).
- `app`: `App.LoginCheck(f)`, a check every login runs once the user is known; an error refuses the
  login with 403 (a `*realtime.ClientError`'s message as `{"error": …}`).
- `Router.Check` keeps working as before; `restrict.Register` runs after any check the brand set.

### Upgrade notes
- New migration in the opt-in `restrict` module only (runs when a brand registers it).
- Support console users gain `restrict.set`.

## [1.1.1] - 2026-10-31

### Added
- `config`: `centrifugo.namespaces` lists the brand's own channel namespaces (`name`, `presence`,
  `subscribe`), which `gamify-config` adds to the platform's `user`, `online` and `feed`. Until now a
  game could only publish to the platform's namespaces: Centrifugo refused any other channel
  (`102 unknown channel`). Names are letters, digits, `-` and `_`; a platform name or a duplicate is
  refused. No change for configs without the key.
- `realtime`: `Broadcast(ctx, transport, channels, data)` sends one message to many channels (a
  match's tick to every player): in one call when the transport implements the new optional
  `Broadcaster` interface, else one `Publish` per channel. A failed channel doesn't stop the
  others; the error names it. The Centrifugo driver implements `Broadcaster` (its `Broadcast` API).
  No change for existing `Transport` implementations.

## [1.1.0] - 2026-10-22

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
  `Ticket`, `Match`, `Player`, `Result` types, `Forever`, client errors 1030-1036 (1036 `ErrClosed`,
  "match closed"), and the OpenSkill
  `Rating`/`DefaultRating`/`Ordinal`. The ledger game ID `matchmaking.Game = 1` is claimed by `Register`.
  A queue needs 2+ teams unless FFA, a non-empty stake list per currency, `NumberRule.Every > 0`,
  and non-negative `FillAfter`/`MatchTimeout`/`MaxWait` (`Forever` allowed). The matcher (internal)
  fills teams anchor first, then larger parties first, oldest first among equal sizes, and falls
  back to a bounded search when that greedy fill misses a feasible packing.
- `matchmaking`: tickets and RPCs. `match.queue {queue, currency, stake, properties, numbers, idem_key}`
  returns a `QueueResult` (exactly one of `Ticket`, `MatchID`, `Removed`); `match.cancel {}`;
  `match.status {}` returns `{ticket, matches}` (the caller's non-final matches); `rating.get {key}`
  returns `{mu, sigma, ordinal, matches}`, and `Service.Rating(ctx, userID, key)` is the Go side.
  A queue call must carry every configured `Numbers` key and every declared `Properties` key and
  nothing else (`gameconfig.ErrInvalid`); `Stakes`/`Game.Check` limits apply (`ErrInvalid`,
  `ErrUnavailable`). The caller queues alone unless they lead a party of 2+ (then the whole party
  queues; with `PartiesPerUser` > 1 the oldest such party they lead), a mere member gets
  `party.ErrNotLeader`, a party larger than every team (FFA: 1) `ErrPartyTooBig`. Unless
  `Concurrent`, a user in a non-final match of the queue gets `ErrBusy`. `idem_key` (required, at
  most 64 bytes) replays the stored outcome even after the ticket is gone and whatever the payload.
  Pushes: `{"type":"match.ticket","ticket"}` and `{"type":"match.ticket_removed","reason"}`
  (`cancelled`, `party_changed`; a party change of any kind removes its ticket, and if that fails
  the next matcher pass removes a ticket whose party is gone or has another leader or members). `Ticket`, `Match`,
  `Player` and `Result` have JSON tags; `Ticket.Server` (set by `QueueConfig.Prepare`, which gets a
  copy of the ticket and only its `Server` is kept) is never sent to clients.
- `matchmaking`: formation. Every `Config.Tick` (`Service.MatchNow(ctx)` runs one pass; one instance
  per queue at a time) drops tickets older than `MaxWait` (`expired`) and, unless `AllowOffline`,
  tickets with a user not in `Realtime.Online` (`offline`), then forms matches (`forming`, deadline
  `MatchTimeout`, players in team then user order, `EntryID` = pot entry ID, ratings snapshotted).
  Staked: every stake is held (`HoldPot`, game `matchmaking.Game`); a refused hold refunds everyone,
  cancels the match (`insufficient_funds`), removes the short users' tickets (`match.ticket_removed`
  `insufficient_funds`) and re-queues the others with their original queue time (`match.ticket`);
  a user who queued again meanwhile keeps that ticket and the old one is removed (`match.ticket_removed`
  `already_queued`).
  Then `QueueConfig.OnMatch` (must be idempotent: it may run again; an error cancels and refunds,
  reason `on_match`, push `{"type":"match.cancelled","match","reason"}`), then `running` and
  `{"type":"match.found","match"}` to every player. A ledger outage leaves the match `forming`;
  the reconcile pass (`Service.Reconcile`) finishes `forming` and `cancelling` matches. A
  `match.queue` replay of a matched ticket returns `{match_id}`. `Service.Get(ctx, id)` returns a
  match (`ErrNotFound`).
- `matchmaking`: `Service.Create(ctx, NewMatch{Queue, Teams, Currency, Stake, Ref})` builds a match without
  tickets (tournament rounds, "challenge a friend") through the same pipeline: stakes held, `OnMatch`,
  running, `match.found`. Teams and stake are validated like a ticket; players need not be online. `Ref`
  (not empty, not `ticket:`-prefixed) makes it idempotent per queue: a repeat returns the existing match.
  A refused hold cancels and refunds it and returns `ledger.ErrInsufficientFunds` (nobody is re-queued);
  `ErrBusy` if the queue is not `Concurrent` and a player is in an open match of it.
- `matchmaking`: results, cancel, ratings, timeouts. `Service.Finish(ctx, id, Result)` (ranks per
  team, one per team, each >= 1) computes the payouts once and stores them with the result
  (`finishing`); settling and every retry pay exactly those, never re-reading the queue config.
  Staked: the pot (stake x players) less the fee (`FeeOf(pot, FeeBP)`, 0 with `NoFee`) goes to the
  best rank's players in equal shares, the remainder one unit each in `EntryID` order, or to
  `QueueConfig.Payout`'s winners (players of the match, adding up to the pot less the fee); an all-equal result in a
  `DrawRefund` queue refunds instead. A payout with no winner or more than `ledger.MaxPotWinners`
  is refused before anything is stored. Then `SettlePotFee`, and in the transaction that marks the
  match `settled`, ranked ratings (from `mm_ratings` as they are at settlement, not the formation
  snapshot) with `matches + 1`. After commit, push
  `{"type":"match.finished","match","payouts":[{user_id,amount}],"ratings":{user_id:{mu,sigma,ordinal}}}`
  (once), then `OnFinish` outside any transaction: at least once (an error or crash makes the
  reconcile pass call it again until it returns nil), must be idempotent, may Finish, Cancel or
  create other matches. The same result again is a no-op, a different one `ErrResultConflict`, a
  cancelled match `ErrClosed`. `Service.Cancel(ctx, id, reason)` refunds a forming or running match
  (`match.cancelled` with the reason); a finishing or settled match is `ErrClosed` (the result
  stands). `Service.Report(ctx, tx, id, Result)` and `Service.MarkCancel(ctx, tx, id, reason)` only
  record, in the caller's transaction (admin actions); the reconcile pass moves the money, settles
  `finishing` matches and cancels forming and running matches past their `MatchTimeout` deadline
  (reason `timeout`). `QueueConfig.FeeBP` must be below 10000.
- `matchmaking`: history, metrics, admin. `Register` adds the activity source `"matchmaking"`: one
  `app.Activity` per player of every staked match that ended (`settled` or `cancelled`; free matches
  are not listed), amount = stake, payout = what the player won, detail
  `{"queue","result":"won|lost|refunded|cancelled"}`. Metrics: `gamify_matchmaking_tickets{queue}`
  (gauge, updated every tick), `gamify_matchmaking_wait_seconds{queue}` (histogram, per ticket when its
  match forms) and `gamify_matchmaking_matches_total{queue,outcome}` (`formed`, `settled`,
  `cancelled`, counted once by the instance that made the transition). New Go reads for admin pages:
  `Service.Matches(ctx, MatchFilter, before, limit)`, `Service.QueueStats(ctx)`,
  `Service.Ratings(ctx, userID, before, limit)`. New package `matchmaking/matchmakingadmin`:
  `Register(adm, s)` adds the views `mm-matches` (actions `cancel`, `report` with ranks like `2,1`; the
  reconcile pass moves the money; reporting a forming match is a 409), `mm-queues` and `mm-ratings`.
- `matchmaking`: the transactions that hold a match row while calling the ledger (holds, refunds,
  settlement) take one of `max(1, pool MaxConns/2)` slots, so concurrent `Finish`/`Cancel`/`Create`
  calls can't take every pool connection and leave none for the Postgres ledger.
- `apptest`: `TEST_DATABASE_URL` may carry pgxpool settings (`pool_max_conns=4`).
- `party`: `OnChange` hooks (and the change pushes) run on a context that the RPC's caller hanging
  up does not cancel. Repeating `party.join` on a closed party keeps the one pending request and
  pushes `party.request` to the leader once. `party.get` answers anyone with the party ID.
- `party`: `Service.List(ctx, userID, before, limit)`, `Service.Disband(ctx, tx, id)` (deletes the
  party in the caller's transaction, a locked one too, and returns it) and `Service.Disbanded(ctx, p)`
  (after the commit: members get `"left":true`, `OnChange` hooks get no members). New package
  `party/partyadmin`: `Register(adm, p)` adds the view `parties` with the action `disband`.
- `admin`: built-in permissions `matchmaking.cancel`, `matchmaking.report` and `party.disband`
  (`PermMatchCancel`, `PermMatchReport`, `PermPartyDisband`), held by operators and owners.
  `Action.After(ctx, call, before, after)` runs once the action's transaction has committed (on a
  context that outlives the request). `Call.Reason` is the action's trimmed reason; actions return
  `admin.BadInput` (400), `admin.Conflict` (409) or `admin.ErrNotFound` (404, also wrapped) for
  operator mistakes instead of a logged 500.

### Upgrade
- `party`: new goose table `goose_party` (migrations run in `party.Register`); nothing changes unless you register it.
- `matchmaking`: new goose table `goose_matchmaking` (migrations run in `matchmaking.Register`, which also
  registers platform ledger game 1); nothing changes unless you register it.
- `admin`: operators and owners now also hold `matchmaking.cancel`, `matchmaking.report` and `party.disband`
  (they show in `/api/me` permissions); they only matter if you register `matchmakingadmin` or `partyadmin`.

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
