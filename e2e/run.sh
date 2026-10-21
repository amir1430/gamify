#!/usr/bin/env bash
# End to end on fresh services: Postgres, TigerBeetle, Centrifugo in Docker, the gamify binary on
# the host, e2e/smoke.mjs as the client. Runs once per ledger driver.
#   e2e/run.sh            # both drivers
#   e2e/run.sh postgres   # one
set -euo pipefail
cd "$(dirname "$0")/.."
DRIVERS=("${@:-postgres tigerbeetle}"); DRIVERS=(${DRIVERS[@]})
P=gamify_e2e; BOT="123:e2e"; JWT_SECRET=e2e-jwt-secret-e2e-jwt-secret-e2e
export CENTRIFUGO_TOKEN_SECRET=e2e-token-secret CENTRIFUGO_API_KEY=e2e-api-key
cleanup() { [[ -n "${APP:-}" ]] && kill "$APP" 2>/dev/null; docker rm -f ${P}_pg ${P}_tb ${P}_cf >/dev/null 2>&1 || true; docker volume rm ${P}_tb >/dev/null 2>&1 || true; [[ -n "${TMP:-}" ]] && rm -rf "$TMP"; true; }
trap cleanup EXIT
cleanup
TMP=$(mktemp -d)

docker run -d --name ${P}_pg -e POSTGRES_PASSWORD=test -p 55433:5432 postgres:17-alpine >/dev/null
docker volume create ${P}_tb >/dev/null
docker run --rm --security-opt seccomp=unconfined -v ${P}_tb:/data ghcr.io/tigerbeetle/tigerbeetle:0.16.46 \
  format --cluster=0 --replica=0 --replica-count=1 /data/0_0.tigerbeetle >/dev/null 2>&1
docker run -d --name ${P}_tb --security-opt seccomp=unconfined --cap-add IPC_LOCK -p 3402:3000 -v ${P}_tb:/data \
  ghcr.io/tigerbeetle/tigerbeetle:0.16.46 start --addresses=0.0.0.0:3000 --cache-grid=256MiB /data/0_0.tigerbeetle >/dev/null
# One gamify.yaml per ledger driver; Centrifugo's config.json is generated from it, as in production.
yaml() {
  cat <<YAML
postgres: {dsn: "\${DATABASE_URL}"}
ledger:
  driver: $1
$([[ $1 == tigerbeetle ]] && echo "  tigerbeetle: {cluster_id: 0, addresses: [\"127.0.0.1:3402\"]}")
centrifugo:
  api_addr: 127.0.0.1:10010
  api_key: \${CENTRIFUGO_API_KEY}
  token_secret: \${CENTRIFUGO_TOKEN_SECRET}
  proxy_listen: :10011
  proxy_endpoint: grpc://host.docker.internal:10011
http: {addr: ":18080", allowed_origins: ["http://localhost"]}
metrics: {addr: "off"}
log: {level: warn}
YAML
}
for drv in postgres tigerbeetle; do yaml $drv > "$TMP/$drv.yaml"; done
DATABASE_URL=unused go run ./cmd/gamify-config -f "$TMP/postgres.yaml" -o "$TMP/centrifugo.json"
docker run -d --name ${P}_cf -p 18000:8000 -p 10010:10010 -v "$TMP/centrifugo.json:/centrifugo/config.json:ro" \
  -e CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY=$CENTRIFUGO_TOKEN_SECRET -e CENTRIFUGO_GRPC_API_KEY=$CENTRIFUGO_API_KEY \
  centrifugo/centrifugo:v6.9.7 centrifugo --config=/centrifugo/config.json >/dev/null
for _ in $(seq 30); do docker exec ${P}_pg pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done

[[ "$(uname)" == Darwin ]] && export CGO_LDFLAGS=-Wl,-ld_classic
go build -o /tmp/${P}_bin ./cmd/gamify 2>&1 | grep -v ld_classic || true

for drv in "${DRIVERS[@]}"; do
  docker exec ${P}_pg psql -U postgres -qc "DROP DATABASE IF EXISTS e2e_$drv" -c "CREATE DATABASE e2e_$drv" >/dev/null
  DATABASE_URL="postgres://postgres:test@127.0.0.1:55433/e2e_$drv?sslmode=disable" GAMIFY_CONFIG="$TMP/$drv.yaml" \
    TG_BOT_TOKEN=$BOT AUTH_JWT_SECRET=$JWT_SECRET AUTH_JWT_AUDIENCE=gamify-e2e SEED_KEY=$(openssl rand -hex 32) DEV_FAUCET=true /tmp/${P}_bin &
  APP=$!
  for _ in $(seq 60); do curl -sf localhost:18080/readyz >/dev/null && break; sleep 0.5; done
  curl -s localhost:18080/readyz; echo
  echo "== $drv"
  API=http://127.0.0.1:18080 WS=ws://127.0.0.1:18000/connection/websocket BOT=$BOT JWT_SECRET=$JWT_SECRET node e2e/smoke.mjs
  kill $APP; wait $APP 2>/dev/null || true; APP=
done
