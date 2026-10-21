# Local stack. Secrets come from .env (cp .env.example .env).
PROFILE ?=
CONFIG  ?= gamify.yaml
ENV      = set -a && . ./.env && set +a

.PHONY: up down config check admin-ui admin-ui-check

up: config
	docker compose $(if $(PROFILE),--profile $(PROFILE)) up --build

down:
	docker compose --profile tigerbeetle down

# Centrifugo's config.json, generated from the same YAML the app reads.
config: $(CONFIG)
	$(ENV) && go run ./cmd/gamify-config -f $(CONFIG) -o infra/centrifugo/config.json

check: $(CONFIG)
	$(ENV) && go run ./cmd/gamify-config -f $(CONFIG) -check

gamify.yaml:
	cp gamify.example.yaml gamify.yaml

# The admin console's UI, built into admin/ui/dist (committed: the Go binary embeds it).
admin-ui:
	npm --prefix admin/ui ci
	npm --prefix admin/ui run build

# Fails if the committed build isn't what the sources build to.
admin-ui-check: admin-ui
	git diff --exit-code admin/ui/dist
	test -z "$$(git status --porcelain admin/ui/dist)"
