# YAML infra config

Oct 21, 2026

## Why

Brands import gamify and write their own Go `main` (games, auth, routes). Their infrastructure
settings, though, are the same for every brand. Today that's ~15 env vars parsed by hand in
`cmd/gamify/main.go`, and Centrifugo's `infra/centrifugo/config.json` is written separately: the
two drift (Centrifugo allows origins `*` while the app keeps its own CORS list; the proxy port is
set in both). One YAML file per environment describes the infra, and both the app and Centrifugo's
config come from it.

## Goals

- A brand's `main` loads `gamify.yaml` and gets ready-built drivers: pool, ledger driver, realtime
  transport.
- Centrifugo's `config.json` is generated from the same file, for Compose now (a mounted file) and
  Kubernetes later (a ConfigMap from the same file).
- Secrets never sit in the YAML or in the generated `config.json`.

## Non-goals

- Business config. Currencies stay in Go (a currency's `Ledger` must never change, so it isn't a
  casual file edit). Game limits stay in `gameconfig` (admin-set, in Postgres).
- Bot token, seed key, `DevFaucet`. They stay in Go for now and move with the auth-provider and
  fairness-module changes (`gamify platform changes.md`).
- Kubernetes manifests, Helm charts, config hot reload, layering several YAML files.

## Anti-goals

- A config that half-works: a typo or an unset secret must stop startup, never fall back to a
  default or an empty string.
- A generated `config.json` that would be unsafe to commit or paste.

## Decided

- `http.allowed_origins` is required and `*` is refused (Centrifugo allowed `*` before).
- The repo ships a `docker-compose.yml`, with `config.json` generated before `up`.

- YAML holds infra only; brands still write Go.
- Secrets are `${VAR}` references, read from the environment.
- Currencies stay in Go.
- The same YAML generates Centrifugo's `config.json`.
- Compose now, Kubernetes later with `config.json` as a ConfigMap.

## The file

One file per environment. All keys below; secrets shown as `${…}`.

```yaml
postgres:
  dsn: ${DATABASE_URL}          # required
  max_conns: 30                 # default 30; a pool_max_conns in the DSN wins, as today

ledger:
  driver: postgres              # postgres (default) | tigerbeetle
  tigerbeetle:                  # required when driver is tigerbeetle, refused otherwise
    cluster_id: 0
    addresses: [tb-0:3000, tb-1:3000, tb-2:3000]

centrifugo:
  api_addr: centrifugo:10000    # required; the app dials it, its port is Centrifugo's grpc_api.port
  api_key: ${CENTRIFUGO_API_KEY}            # required
  token_secret: ${CENTRIFUGO_TOKEN_SECRET}  # required
  token_ttl: 1h                 # default 1h
  proxy_listen: :10001          # default :10001; where the app serves Centrifugo's RPC proxy
  proxy_endpoint: grpc://api:10001          # required; how Centrifugo reaches that proxy
  proxy_timeout: 5s             # default 5s

http:
  addr: :8080                   # default :8080
  allowed_origins: [https://mini.example]   # required, at least one; "*" refused. App CORS and
                                            # Centrifugo client.allowed_origins. Local dev lists
                                            # http://localhost:5173 or similar

metrics:
  addr: :9090                   # default :9090; "off" disables

log:
  level: info                   # debug | info | warn | error
```

## Loading

- `${VAR}` is expanded per string value after parsing, never in the raw text (my call): a secret
  containing ` #`, `: ` or a leading `*`/`[` can't change the YAML around it or get cut off, and a
  parse error can never quote an expanded secret. An unset or empty variable is an error naming it.
  No `${VAR:-default}`: defaults live in the YAML. `$$` escapes a literal `$`.
- Unknown keys are errors (strict decoding).
- Validation collects every problem and returns them together, each with its path:
  `centrifugo.api_key: ${CENTRIFUGO_API_KEY} is not set`, `ledger.driver: "tigerbeetl": want
  postgres or tigerbeetle`.
- Errors never print a secret's value, only the variable name.

## Go API

Two packages (my call), so validating a YAML and generating Centrifugo's config need no cgo: the
TigerBeetle client is cgo and needs `-ld_classic` on macOS.

- `config`: `Load`, validation, `App`, `CentrifugoJSON`. Imports no driver.
- `config/drivers`: `Build`, which imports the ledger and realtime drivers.

`cmd/gamify-config` imports only `config`. Names may change:

```go
type Config struct { Postgres …; Ledger …; Centrifugo …; HTTP …; Metrics …; Log … }

// Load reads, expands, decodes strictly, applies defaults and validates.
func Load(path string) (*Config, error)

// package drivers: Build makes the pool, the ledger driver (running the Postgres ledger's
// migrations) and the Centrifugo transport. close releases them in reverse order.
func Build(ctx context.Context, c *config.Config) (d app.Drivers, close func(), err error)

// App fills the infra part of app.Config (addresses, origins); the brand adds currencies,
// bot token, sealer.
func (c *Config) App() app.Config

// CentrifugoJSON is Centrifugo's config.json for this file, without secrets.
func (c *Config) CentrifugoJSON() []byte
```

Logging setup stays in the brand's `main`; `Config.Log.Level` is a parsed `slog.Level` it can use.

## Generated config.json

Equivalent to today's file, with these fields from the YAML:

| Centrifugo key | From |
| --- | --- |
| `grpc_api.port` | port of `centrifugo.api_addr` |
| `client.allowed_origins` | `http.allowed_origins` |
| `rpc.proxy.endpoint` | `centrifugo.proxy_endpoint` |
| `rpc.proxy.timeout` | `centrifugo.proxy_timeout` |

Fixed by the platform (our code depends on them): `health.enabled`, `grpc_api.enabled`,
namespaces `user`, `online` (presence), `feed` (client subscribe), and
`rpc.without_namespace.proxy_enabled`.

Never in the file: `grpc_api.key`, `client.token.hmac_secret_key`. Centrifugo reads them from its
own env (`CENTRIFUGO_GRPC_API_KEY`, `CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY`), as
`infra/centrifugo/Dockerfile` already documents; deployment sets them to the same values as the
app's `${…}`.

Output is stable (fixed key order), so regenerating an unchanged YAML gives an identical file and a
ConfigMap diff only shows real changes.

## Command

`cmd/gamify-config` needs only the YAML, not the brand's code:

```sh
go run github.com/amir1430/gamify/cmd/gamify-config -f gamify.yaml > config.json   # Centrifugo config
go run github.com/amir1430/gamify/cmd/gamify-config -f gamify.yaml -check          # validate only
```

It needs the secret variables set like the app does (validation is the same), so a CI check runs
with dummy values.

## Changes to existing code

- `cmd/gamify`: replaces the infra env parsing with `config.Load(envOr("GAMIFY_CONFIG",
  "gamify.yaml"))`; keeps `TG_BOT_TOKEN`, `SEED_KEY`, `SEED_KEY_ID`, `CURRENCIES`, `DEV_FAUCET` as
  env until those modules move.
- A sample `gamify.example.yaml` at the repo root, documented in the README.
- `infra/centrifugo`: `config.json` becomes generated, deleted from the repo and gitignored; the
  Dockerfile stops baking it in. Keep the env-secrets comment.
- New `docker-compose.yml` at the repo root: app (built from `cmd/gamify`), Postgres, Centrifugo,
  and TigerBeetle under a `tigerbeetle` profile. It mounts `gamify.yaml` into the app and the
  generated `config.json` into Centrifugo, and passes the secrets as env to both. A Makefile target
  (`make up`) runs `gamify-config` and then `docker compose up`; Compose can't run a step before
  `up` on its own.
- `e2e/run.sh`: writes a test YAML, generates Centrifugo's config from it and mounts that, so e2e
  covers the generator. Centrifugo's API port is the same inside and outside the container (today
  10000 is mapped to 10010), since `grpc_api.port` comes from `api_addr`.

## Tests

- Load: a full file, defaults, an unknown key, an unset `${VAR}`, tigerbeetle section with the
  postgres driver, several errors reported together, and no secret value in any error.
- `CentrifugoJSON`: golden file matching today's `config.json` semantics for the sample YAML; no
  secret field present.
- e2e as above, both ledger drivers.

## Left to the builder

YAML library (prefer a maintained one such as `go.yaml.in/yaml/v3`), package and field names, the
exact error text, how `Drivers` is split internally.
