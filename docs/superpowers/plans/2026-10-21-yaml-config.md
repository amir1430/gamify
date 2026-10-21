# YAML Infra Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Brands load infra settings from one `gamify.yaml` (secrets as `${VAR}`), get ready-built drivers from it, and generate Centrifugo's `config.json` from the same file.

**Architecture:** A cgo-free `config` package loads, expands, validates and renders Centrifugo's JSON; `config/drivers` turns a `*config.Config` into `app.Drivers`. `cmd/gamify-config` (imports only `config`) validates and generates; `cmd/gamify` and e2e switch to the YAML; a root `docker-compose.yml` + `make up` run it locally.

**Tech Stack:** Go 1.27, `go.yaml.in/yaml/v3`, pgx v5, Centrifugo v6.9.7, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-10-21-yaml-config-design.md`

## Global Constraints

- Secrets only as `${VAR}`; never in the YAML, the generated `config.json`, or any error message (errors name the variable, never its value).
- `${VAR}` expands per string value after parsing, never in raw text. Unset or empty → error. `$$` → literal `$`. No `${VAR:-default}`.
- Unknown YAML keys are errors. All validation errors are returned together, each prefixed by its YAML path (`centrifugo.api_key: …`).
- Defaults: `postgres.max_conns` 30 (a `pool_max_conns` in the DSN wins), `ledger.driver` postgres, `centrifugo.token_ttl` 1h, `centrifugo.proxy_listen` :10001, `centrifugo.proxy_timeout` 5s, `http.addr` :8080, `metrics.addr` :9090 (`off` disables), `log.level` info.
- Required: `postgres.dsn`, `centrifugo.api_addr`, `centrifugo.api_key`, `centrifugo.token_secret`, `centrifugo.proxy_endpoint`, `http.allowed_origins` (≥ 1, `*` refused). `ledger.tigerbeetle` required iff driver is `tigerbeetle`, refused otherwise.
- `config` imports no driver package (no cgo). macOS builds: `CGO_LDFLAGS=-Wl,-ld_classic`.
- Currencies, bot token, seed key, `DEV_FAUCET` stay env/Go in `cmd/gamify`.
- Nothing is committed yet; the repo has no commits. No commit steps: the user commits.

## Review Focus

1. A secret containing ` #`, `: `, `*` or `"` must come through byte-for-byte (Task 1 test `TestSecretsVerbatim`).
2. A YAML syntax error near a `${VAR}` must not print the variable's value (Task 1 test `TestNoSecretInErrors`).
3. `api_addr` without a port or with a non-numeric port must be a validation error, not a broken `grpc_api.port` (Task 1 test `TestValidate`).
4. An empty file or a file that is only comments must report every required field, not panic (Task 1 test `TestValidate`).
5. Regenerating from an unchanged YAML must give byte-identical JSON (Task 2 test `TestCentrifugoJSONStable`).

---

### Task 1: `config` package: Load, expand, defaults, validate

**Files:**
- Create: `config/config.go`, `config/config_test.go`, `config/testdata/full.yaml`
- Modify: `go.mod` (add `go.yaml.in/yaml/v3`)

**Interfaces:**
- Produces:
  ```go
  type Config struct {
  	Postgres   Postgres   `yaml:"postgres"`
  	Ledger     Ledger     `yaml:"ledger"`
  	Centrifugo Centrifugo `yaml:"centrifugo"`
  	HTTP       HTTP       `yaml:"http"`
  	Metrics    Metrics    `yaml:"metrics"`
  	Log        Log        `yaml:"log"`
  }
  type Postgres struct { DSN string `yaml:"dsn"`; MaxConns int32 `yaml:"max_conns"` }
  type Ledger struct { Driver string `yaml:"driver"`; TigerBeetle *TigerBeetle `yaml:"tigerbeetle"` }
  type TigerBeetle struct { ClusterID uint64 `yaml:"cluster_id"`; Addresses []string `yaml:"addresses"` }
  type Centrifugo struct {
  	APIAddr string `yaml:"api_addr"`; APIKey string `yaml:"api_key"`; TokenSecret string `yaml:"token_secret"`
  	TokenTTL time.Duration `yaml:"token_ttl"`; ProxyListen string `yaml:"proxy_listen"`
  	ProxyEndpoint string `yaml:"proxy_endpoint"`; ProxyTimeout time.Duration `yaml:"proxy_timeout"`
  }
  type HTTP struct { Addr string `yaml:"addr"`; AllowedOrigins []string `yaml:"allowed_origins"` }
  type Metrics struct { Addr string `yaml:"addr"` }
  type Log struct { Level slog.Level `yaml:"level"` }

  func Load(path string) (*Config, error)              // reads the file, calls Parse with os.LookupEnv
  func Parse(data []byte, env func(string) (string, bool)) (*Config, error)
  func (c *Config) App() app.Config                     // HTTPAddr, MetricsAddr, AllowedOrigins only
  ```
  `app` is cgo-free (it imports `ledger` and `realtime` interfaces only); confirm with `go list -deps ./config | grep tigerbeetle` → empty.

- [ ] **Step 1: Write the failing tests** in `config/config_test.go`, all via `Parse` with a map-backed `env`:
  - `TestFull`: `testdata/full.yaml` (every key from the spec's sample, tigerbeetle driver, 3 addresses) with env `{DATABASE_URL: "postgres://u:p@h/db", CENTRIFUGO_API_KEY: "k", CENTRIFUGO_TOKEN_SECRET: "s"}` → every field equals the file's value; `Log.Level == slog.LevelInfo`; `TokenTTL == time.Hour`.
  - `TestDefaults`: only required keys → `MaxConns 30`, `Driver "postgres"`, `TokenTTL 1h`, `ProxyListen ":10001"`, `ProxyTimeout 5s`, `HTTP.Addr ":8080"`, `Metrics.Addr ":9090"`, `Level info`.
  - `TestSecretsVerbatim`: `CENTRIFUGO_API_KEY = "a #b: *c \"d"` → `APIKey` equals it exactly; `api_key: "x$$y"` → `"x$y"`.
  - `TestValidate`: table of (yaml, env, substrings the error must contain): unknown key `postgres.max_conn` → `max_conn`; unset `${CENTRIFUGO_API_KEY}` → `centrifugo.api_key: ${CENTRIFUGO_API_KEY} is not set`; driver `tigerbeetl` → `ledger.driver`; tigerbeetle section with postgres driver → `ledger.tigerbeetle`; tigerbeetle driver without section → `ledger.tigerbeetle`; `allowed_origins: ["*"]` → `http.allowed_origins`; missing `allowed_origins` → `http.allowed_origins`; `api_addr: centrifugo` → `centrifugo.api_addr`; empty file → contains all six required paths at once.
  - `TestNoSecretInErrors`: env value `"SUPERSECRET"`, yaml with both a validation error and a bad duration `token_ttl: soon` → error mentions neither `SUPERSECRET`; a broken YAML (`centrifugo: [`) with the same env → error lacks `SUPERSECRET`.

- [ ] **Step 2: Run** `go test ./config/` → FAIL (package doesn't exist).

- [ ] **Step 3: Implement** in `config/config.go`. Approach: strict-decode the raw bytes (`yaml.NewDecoder` + `KnownFields(true)`; an empty document decodes to the zero `Config`), apply defaults, then walk the struct with `reflect`, expanding `${VAR}` in every `string` and `[]string` field and building each field's path from its `yaml` tag; collect expansion and validation errors into one `errors.Join`. Expansion: scan for `$$` → `$` and `${NAME}` → `env(NAME)`; any other `$` is left as is. `api_addr` must split with `net.SplitHostPort` into a numeric port. `Log.Level` decodes via `slog.Level.UnmarshalText` (yaml/v3 honors `encoding.TextUnmarshaler`).

- [ ] **Step 4: Run** `go test ./config/ && go vet ./config/` → PASS.

### Task 2: Centrifugo `config.json` generation

**Files:**
- Create: `config/centrifugo.go`, `config/centrifugo_test.go`, `config/testdata/centrifugo.golden.json`

**Interfaces:**
- Consumes: `Config` from Task 1.
- Produces: `func (c *Config) CentrifugoJSON() []byte` (indented 2 spaces, trailing newline).

- [ ] **Step 1: Write the failing tests:**
  - `TestCentrifugoJSON`: `testdata/full.yaml` (`api_addr centrifugo:10000`, origins `[https://mini.example]`, `proxy_endpoint grpc://api:10001`, timeout 5s) → output equals the golden file, whose content is today's `infra/centrifugo/config.json` with `client.allowed_origins: ["https://mini.example"]`, `grpc_api.port: 10000`, `rpc.proxy.endpoint`, `rpc.proxy.timeout: "5s"`. Regenerate the golden with `-update` flag.
  - `TestCentrifugoJSONStable`: two calls → byte-identical.
  - `TestCentrifugoJSONNoSecrets`: output contains neither the api key nor the token secret values, nor the keys `"key"` or `"hmac_secret_key"`.

- [ ] **Step 2: Run** `go test ./config/ -run Centrifugo` → FAIL.

- [ ] **Step 3: Implement** with Go structs (not maps) so `encoding/json` keeps field order; fixed fields as in the spec: `health.enabled`, `grpc_api.enabled`, namespaces `user`, `online` (`presence: true`), `feed` (`allow_subscribe_for_client: true`), `rpc.without_namespace.proxy_enabled`. Timeout rendered with `time.Duration.String()`.

- [ ] **Step 4: Run** `go test ./config/` → PASS.

### Task 3: `config/drivers`

**Files:**
- Create: `config/drivers/drivers.go`, `config/drivers/drivers_test.go`

**Interfaces:**
- Consumes: `config.Config`.
- Produces: `func Build(ctx context.Context, c *config.Config) (app.Drivers, func(), error)`. Logic moves from `cmd/gamify/main.go`: pool (`MaxConns` unless DSN has `pool_max_conns`), ledger (`postgres`: `db.Migrate(…, postgres.Migrations, "goose_ledger")` + `postgres.New`; `tigerbeetle`: `tigerbeetle.Dial(ctx, ClusterID, strings.Join(Addresses, ","))`), `centrifugo.New` with `TokenTTL` and `ProxyAddr: ProxyListen`. On error, everything already opened is closed; the returned func closes in reverse order.

- [ ] **Step 1: Write the failing test** `TestBuildIntegration` (skipped without `TEST_DATABASE_URL`): Postgres driver, Centrifugo at an unused address (`centrifugo.New` doesn't dial) → no error, `d.Pool.Ping` ok, `d.Ledger` non-nil; calling close twice doesn't panic.
- [ ] **Step 2: Run** `TEST_DATABASE_URL=… go test ./config/drivers/` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** it again → PASS; `go list -deps ./config | grep -c tigerbeetle` → `0`.

### Task 4: Commands and sample file

**Files:**
- Create: `cmd/gamify-config/main.go`, `gamify.example.yaml`
- Modify: `cmd/gamify/main.go`, `README.md` (Run section, layout table rows for `config/`, `config/drivers/`, `cmd/gamify-config/`)

**Interfaces:**
- Consumes: `config.Load`, `(*Config).CentrifugoJSON`, `(*Config).App`, `drivers.Build`.

- [ ] **Step 1: `cmd/gamify-config`:** flags `-f` (default `gamify.yaml`), `-check` (validate only, prints nothing on success), `-o` (output file; default stdout). Exit 1 with the joined errors on stderr.
- [ ] **Step 2: `cmd/gamify`:** `config.Load(envOr("GAMIFY_CONFIG", "gamify.yaml"))`, log level from `cfg.Log.Level`, `drivers.Build`, `app.Config` from `cfg.App()` plus `Currencies`, `BotToken`, `Sealer`, `DevFaucet` from env as today. Remove `DATABASE_URL`, `LEDGER_DRIVER`, `TB_*`, `REALTIME_DRIVER`, `CENTRIFUGO_*`, `PROXY_GRPC_ADDR`, `HTTP_ADDR`, `METRICS_ADDR`, `ALLOWED_ORIGINS`, `LOG_LEVEL` handling.
- [ ] **Step 3: `gamify.example.yaml`:** the spec's sample, Compose hostnames (`postgres`, `centrifugo:10000`, `grpc://app:10001`), origin `http://localhost:5173`.
- [ ] **Step 4: Verify:** `go build ./... && go vet ./...`; `DATABASE_URL=x CENTRIFUGO_API_KEY=x CENTRIFUGO_TOKEN_SECRET=x go run ./cmd/gamify-config -f gamify.example.yaml -check` → exit 0; with `CENTRIFUGO_API_KEY` unset → exit 1 naming `centrifugo.api_key`; `CGO_ENABLED=0 go build ./cmd/gamify-config` → succeeds.

### Task 5: Compose, Centrifugo image, e2e

**Files:**
- Create: `docker-compose.yml`, `Makefile`, `Dockerfile` (app: multi-stage, cgo for TigerBeetle, builds `./cmd/gamify`), `.env.example`
- Modify: `infra/centrifugo/Dockerfile` (drop `COPY config.json`), `.gitignore` (`/infra/centrifugo/config.json`, `/gamify.yaml`), `e2e/run.sh`
- Delete: `infra/centrifugo/config.json`

- [ ] **Step 1: Compose:** services `postgres` (17-alpine), `centrifugo` (mounts `./infra/centrifugo/config.json:/centrifugo/config.json:ro`, env `CENTRIFUGO_GRPC_API_KEY=${CENTRIFUGO_API_KEY}`, `CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY=${CENTRIFUGO_TOKEN_SECRET}`), `app` (built from `Dockerfile`, mounts `./gamify.yaml`, env `DATABASE_URL`, `CENTRIFUGO_API_KEY`, `CENTRIFUGO_TOKEN_SECRET`, `TG_BOT_TOKEN`, `SEED_KEY`), `tigerbeetle` under profile `tigerbeetle` (format + start, as in `e2e/run.sh`). Secrets read from `.env` (gitignored; `.env.example` lists them with dummy values).
- [ ] **Step 2: Makefile:** `config` runs `go run ./cmd/gamify-config -f gamify.yaml -o infra/centrifugo/config.json`; `up: config` then `docker compose up --build`; `check` runs `-check`. Copies `gamify.example.yaml` to `gamify.yaml` if missing.
- [ ] **Step 3: e2e:** `run.sh` writes `$TMP/gamify.yaml` per driver (ports 55433, 3402, Centrifugo API `10010:10010` with `api_addr 127.0.0.1:10010`, `proxy_listen :10011`, `proxy_endpoint grpc://host.docker.internal:10011`, `http.addr :18080`, `metrics.addr off`, origin `http://localhost`), generates Centrifugo's config into `$TMP` before starting Centrifugo and mounts it, and starts the binary with `GAMIFY_CONFIG`. Centrifugo needs one config for both drivers: generate it from the postgres YAML.
- [ ] **Step 4: Verify:** `e2e/run.sh` → `smoke ok` for postgres and tigerbeetle; `make check` with `.env` sourced → exit 0; `docker compose config` → valid.
