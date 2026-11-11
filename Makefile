XOLO_DIR ?= ../xolo
COMPOSE = docker compose -f deploy/docker-compose.yml
REPLICAS ?= 1

# Database target. The postgres URL points to the compose instance, exposed on
# the host; a file path switches cmd/seed and the SQL script to SQLite.
PG_PORT ?= 55432
DB_DSN ?= postgres://xolo:xolo@localhost:$(PG_PORT)/xolo?sslmode=disable

# Address of the chaos-provider as seen from Xolo: the compose service name by
# default, localhost when Xolo runs on the host.
CHAOS_PROVIDER_URL ?= http://chaos-provider:9090/v1
SEED_DAYS ?= 30

# Rate limit and retry applied to the chaos provider. Both are disabled by
# default: a limiter caps the campaign, and retries hide the injected faults.
# Turn them back on when they are what you are testing, for example
#   make seed CHAOS_RATE_LIMIT='{"enabled":true,"interval":1000000000,"max_burst":50}'
CHAOS_RATE_LIMIT ?= {"enabled":false}
CHAOS_RETRY ?= {"enabled":false}

IS_PG := $(if $(filter postgres%,$(DB_DSN)),1,)
SQL = $(if $(IS_PG),$(COMPOSE) exec -T postgres psql -q -v ON_ERROR_STOP=1 -U xolo -d xolo,sqlite3 '$(DB_DSN)')

.PHONY: build test seed db up down logs targets run-% k6-% provider clean

build: bin/chaos-provider bin/plugins/chaos-monkey

bin/chaos-provider: $(shell find cmd internal -name '*.go') go.mod
	CGO_ENABLED=0 go build -o $@ ./cmd/chaos-provider

# Plugins live in their own directory: XOLO_PLUGINS_DIR executes everything it
# finds there, so the provider binary must not be in it.
bin/plugins/chaos-monkey: $(shell find plugins internal -name '*.go') go.mod
	@mkdir -p $(@D)
	CGO_ENABLED=0 go build -o $@ ./plugins/chaos-monkey

test:
	go test ./...

# Starts the database alone and waits until it answers.
db:
ifdef IS_PG
	$(COMPOSE) up -d postgres
	@echo "waiting for PostgreSQL..."
	@for i in $$(seq 1 40); do \
		$(COMPOSE) exec -T postgres pg_isready -U xolo -d xolo >/dev/null 2>&1 && exit 0; \
		sleep 1; \
	done; echo "PostgreSQL did not start" >&2; exit 1
endif

# Generates the Xolo dataset from the sibling repository, then adds the chaos
# provider, its models, the middleware and the metrics token to it.
seed: db
	cd $(XOLO_DIR) && go run ./cmd/seed -dsn '$(DB_DSN)' -force -days $(SEED_DAYS)
	sed -e 's|@@PROVIDER_URL@@|$(CHAOS_PROVIDER_URL)|' \
	    -e 's|@@RATE_LIMIT@@|$(CHAOS_RATE_LIMIT)|' \
	    -e 's|@@RETRY@@|$(CHAOS_RETRY)|' deploy/prepare-db.sql \
	  | $(SQL)
	$(SQL) < deploy/pipelines.sql
	@echo "database ready: $(DB_DSN) (provider → $(CHAOS_PROVIDER_URL))"

up: build
	$(COMPOSE) up -d --build --scale xolo=$(REPLICAS)
	@echo "load balancer http://localhost:3002 ($(REPLICAS) instance(s)) — provider http://localhost:9090 — Prometheus http://localhost:9091"

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f xolo lb chaos-provider

# Requests served by each instance: the proof that the load balancer spreads the load.
targets:
	@python3 deploy/targets.py http://localhost:9091

# Runs a campaign inside the compose network.
run-%:
	$(COMPOSE) run --rm k6 run --out experimental-prometheus-rw /scripts/$*.js

# Runs a campaign with a local k6 against a Xolo on the host.
k6-%:
	XOLO_URL=$${XOLO_URL:-http://localhost:3002} k6 run k6/$*.js

# Runs chaos-provider on the host, for a Xolo started from $(XOLO_DIR).
provider: bin/chaos-provider
	CHAOS_ADDR=:9090 CHAOS_SEED=$${CHAOS_SEED:-20260914} bin/chaos-provider

clean:
	rm -rf bin
