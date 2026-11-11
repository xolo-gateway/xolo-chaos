# xolo-chaos

Load and fault-injection bench for [Xolo](https://github.com/xolo-gateway/xolo).
It holds a fake LLM provider, a Xolo plugin that injects faults, k6 load
scripts, and a Docker Compose stack that runs them together.

## Contents

```
cmd/chaos-provider/   fake OpenAI-compatible provider
internal/chaos/       fault profiles, random distributions, filler text
plugins/chaos-monkey/ Xolo plugin that injects faults into the plugin subsystem
k6/                   load scripts and their shared library
deploy/               Compose stack, load balancer, Prometheus, SQL setup
Makefile              build, seed, start the stack, run the scripts
```

## chaos-provider

An OpenAI-compatible server (`POST /v1/chat/completions`, `GET /v1/models`)
that misbehaves on purpose. Xolo registers it as an `openai` provider. The
model name selects a fault profile, so `chaos-slow` uses the `slow` profile.

| Profile | Behaviour |
|---|---|
| `fast` | near-immediate answer, realistic token usage |
| `slow` | median time to first token of 800 ms with a long tail, 30 tokens/s |
| `flaky` | 15% of 429, 503 and 500 answers, with `Retry-After` on 429 and 503 |
| `hangup` | one stream in three cut after 3 to 20 chunks |
| `stall` | accepts the request and never answers |
| `garbage` | one answer in four malformed (truncated JSON, an SSE event that is not JSON) |
| `mixed` | variable latency, 5% errors, 3% cut streams, 2% malformed answers |

A profile is a JSON object, defined in `internal/chaos/profile.go`. It sets
the latency (log-normal, `median_ms`, `sigma`, `max_ms`), the generation
speed, the number of tokens, and the error, hangup, stall and garbage rates.

Configuration, through environment variables:

| Variable | Default | Effect |
|---|---|---|
| `CHAOS_ADDR` | `:9090` | listen address |
| `CHAOS_PROFILES` | built-in profiles | JSON file that replaces the profile list |
| `CHAOS_SEED` | current time | seed of the random generator; the same seed and the same traffic give the same faults |
| `CHAOS_LOG_LEVEL` | `info` | log level |

Other endpoints:

- `GET /admin/profiles`, `PUT /admin/profiles/{name}` and
  `DELETE /admin/profiles/{name}` read and change the profiles while the
  server runs.
- `GET /metrics` serves Prometheus metrics: requests by profile and outcome
  (`ok`, `error`, `hangup`, `stall`, `garbage`, `cancelled`), and a duration
  histogram.
- `GET /healthz` answers `ok`.

```bash
curl -X PUT localhost:9090/admin/profiles/fast \
  -d '{"ttft":{"median_ms":20},"completion_tokens":{"min":40,"max":120},"error":{"rate":0.5,"statuses":[503]}}'
```

## chaos-monkey

A Xolo plugin (`PRE_REQUEST` hook) that injects faults into Xolo's plugin
subsystem. HTTP errors and stream faults come from chaos-provider; this
plugin covers what only a plugin can do. Each pipeline node that uses it
takes this configuration:

| Field | Effect |
|---|---|
| `latency` | log-normal delay before the plugin answers |
| `reject_rate` | probability of rejecting the request (403 for the client) |
| `error_rate` | probability of answering with a gRPC error |
| `crash_rate` | probability of exiting the process, to test Xolo's plugin restart |

Xolo runs every executable of `XOLO_PLUGINS_DIR`, so the Makefile builds the
plugin into `bin/plugins/`, apart from `bin/chaos-provider`.

## k6 scripts

The scripts send requests at a set arrival rate, whatever the response time,
as LLM clients do. They report the time to first byte and the truncated
streams, those that end without `[DONE]`. `k6/lib/xolo.js` holds the shared
client.

| Script | Purpose | Variables |
|---|---|---|
| `nominal.js` | one fixed rate, streamed and not, with latency and error thresholds | `RATE`, `DURATION` |
| `rupture.js` | a rising rate, aborted at 5% failures, to find the breaking point | `PEAK`, `MODEL` |
| `profile.js` | one model at a fixed rate, without thresholds, to measure what Xolo consumes | `MODEL`, `RATE`, `DURATION`, `STREAM`, `PROMPT_SET` |
| `mix.js` | every fault profile at once for a long run, with thresholds per model | `DURATION` |

Every script reads `XOLO_URL`, `XOLO_TOKEN` and `XOLO_ORG`. `PROMPT_SET=pii`
sends prompts that contain names, addresses and companies, to exercise the
pseudonymizer. The prompts are in French on purpose, because the
pseudonymizer under test handles French text.

The thresholds in the scripts suit a local stack. Adjust them to the target
before relying on them.

## Compose stack

`deploy/docker-compose.yml` runs:

- PostgreSQL, with `fsync` off, since the data is disposable;
- chaos-provider;
- one or more Xolo instances, built from the Xolo repository, with the
  chaos-monkey plugin;
- Caddy as a load balancer in front of the Xolo instances, on port 3002;
- Prometheus on port 9091, which scrapes each Xolo instance, chaos-provider
  and Caddy, and receives the k6 metrics;
- k6, to run the scripts inside the Compose network.

`deploy/prepare-db.sql` adds to Xolo's dataset the chaos provider, one model
per profile, a chaos-monkey middleware and an API token for Prometheus.
`deploy/pipelines.sql` adds three models that put a pipeline in front of the
`fast` profile: `chaos-pseudo` (pseudonymizer), `chaos-guard` (prompt guard)
and `chaos-smart` (complexity-based routing). Both scripts run on SQLite and
on PostgreSQL.

## Usage

Requirements: Go, GNU make, Docker, and the Xolo repository checked out next
to this one (`../xolo`, or set `XOLO_DIR`). The Makefile generates the
dataset with Xolo's `cmd/seed`.

```bash
make seed              # start PostgreSQL, generate the dataset, add the chaos setup
make up                # build and start the stack with one Xolo instance
make up REPLICAS=3     # three Xolo instances behind the load balancer
make run-nominal       # run k6/nominal.js inside the Compose network
make run-rupture
make run-mix
make targets           # requests served by each Xolo instance
make down
```

| Variable | Default | Effect |
|---|---|---|
| `REPLICAS` | `1` | number of Xolo instances |
| `DB_DSN` | the Compose PostgreSQL | database to seed; a file path selects SQLite |
| `PG_PORT` | `55432` | host port of PostgreSQL |
| `CHAOS_PROVIDER_URL` | `http://chaos-provider:9090/v1` | provider URL as Xolo sees it |
| `CHAOS_RATE_LIMIT`, `CHAOS_RETRY` | disabled | rate limit and retry of the chaos provider in Xolo |
| `SEED_DAYS` | `30` | days of usage history in the dataset |
| `XOLO_CPUS`, `XOLO_MEM` | `0.5`, `512m` | resources of each Xolo instance |
| `XOLO_TOKEN` | `xolo-e2e-alice-acme` | API token k6 uses, from Xolo's dataset |

The rate limit and retry are off by default: a limiter would cap the load,
and retries would hide the injected faults.

To run without Docker, start chaos-provider on the host and point the
dataset at it:

```bash
make seed DB_DSN=$PWD/deploy/data/data.sqlite CHAOS_PROVIDER_URL=http://localhost:9090/v1
make build provider
# then start Xolo on that database with XOLO_PLUGINS_DIR set to bin/plugins,
# and run a script with a local k6: make k6-nominal
```

`DB_DSN` must be an absolute path: `cmd/seed` runs from the Xolo checkout.

Xolo's dataset encrypts its provider keys with a fixed `XOLO_SECRET_KEY`,
set in `deploy/docker-compose.yml`. A Xolo started by hand needs the same
key. It is public: never use it outside this bench.

## Plugin SDK

The plugin depends on Xolo's `pkg/pluginsdk`. Its latest tag,
`pkg/pluginsdk/v0.76.0`, speaks plugin protocol 2, which current Xolo
refuses. Until a newer tag exists, `go.mod` resolves the SDK from the Xolo
checkout next to this one, with a `replace`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Contributions require a DCO sign-off
(`git commit -s`).

## License

Apache License 2.0, see [LICENSE](LICENSE).
