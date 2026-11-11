# chaos-provider

chaos-provider is an OpenAI-compatible chat completion server that misbehaves
on purpose. Point a client, an SDK, a proxy or an LLM gateway at it to see how
it copes with slow models, rate limits, server errors, cut streams, malformed
answers and requests that never end.

It does not run a model. Answers are random English words, with realistic
token counts and timings. Every fault is drawn from a seeded random
generator, so a run can be replayed.

## Install

Download the archive for your system from the
[releases](https://github.com/xolo-gateway/xolo-chaos/releases), then:

```bash
tar xzf chaos-provider_*_linux_amd64.tar.gz
./chaos-provider --version
```

Or run the image:

```bash
docker run --rm -p 9090:9090 ghcr.io/xolo-gateway/chaos-provider:latest
```

Or build it from the sources, with Go:

```bash
git clone https://github.com/xolo-gateway/xolo-chaos
cd xolo-chaos
go build ./cmd/chaos-provider
```

## Run

```bash
./chaos-provider
```

It listens on port 9090. The API lives under `/v1`:

| Endpoint | Role |
|---|---|
| `POST /v1/chat/completions` | chat completion, streamed (`"stream": true`) or not |
| `GET /v1/models` | the models it serves, one per profile |

It accepts any API key, or none.

```bash
curl -s localhost:9090/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"chaos-slow","stream":true,"messages":[{"role":"user","content":"Hello"}]}'
```

With the OpenAI Python SDK:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:9090/v1", api_key="unused")
stream = client.chat.completions.create(
    model="chaos-flaky",
    messages=[{"role": "user", "content": "Hello"}],
    stream=True,
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="")
```

## Profiles

The model name selects a profile: `chaos-slow`, or `slow`, uses the `slow`
profile. An unknown model gets a 404, as with OpenAI.

| Model | Behaviour |
|---|---|
| `chaos-fast` | answers in about 20 ms |
| `chaos-slow` | median time to first token of 800 ms, with a long tail up to 10 s, then 30 tokens/s |
| `chaos-flaky` | 15% of the requests fail with 429, 503 or 500; 429 and 503 carry `Retry-After: 2` |
| `chaos-hangup` | one request in three loses its connection after 3 to 20 stream chunks |
| `chaos-stall` | accepts the request and never answers, until the client gives up |
| `chaos-garbage` | one answer in four is malformed: truncated JSON, or an SSE event that is not JSON |
| `chaos-mixed` | latency with a long tail, 5% errors, 3% cut connections, 2% malformed answers |

A streamed answer ends with a chunk that carries the token usage, then
`data: [DONE]`, as OpenAI does. Set `"stream_options": {"include_usage": false}`
to leave the usage chunk out. A cut stream ends without `[DONE]`.

## Custom profiles

A profile is a JSON object:

```json
{
  "name": "overloaded",
  "description": "A busy model that sheds load",
  "ttft": {"median_ms": 1500, "sigma": 0.8, "max_ms": 20000},
  "tokens_per_sec": 20,
  "completion_tokens": {"min": 50, "max": 400},
  "error": {"rate": 0.3, "statuses": [429, 503], "retry_after_s": 5},
  "hangup": {"rate": 0.05, "after_chunks": {"min": 1, "max": 50}},
  "garbage_rate": 0.01
}
```

| Field | Meaning |
|---|---|
| `ttft` | delay before the first byte, log-normal: `median_ms`, `sigma` (0 for a fixed delay, 1 for a long tail), `max_ms` to cap it |
| `tokens_per_sec` | generation speed; 0 sends the whole answer at once |
| `completion_tokens` | number of words in the answer, drawn between `min` and `max` |
| `error.rate` | probability of failing before any content, between 0 and 1 |
| `error.statuses` | status codes to fail with, drawn evenly; 503 when empty |
| `error.retry_after_s` | `Retry-After` header added to 429 and 503 |
| `hangup.rate` | probability of dropping the connection mid-answer |
| `hangup.after_chunks` | stream chunks sent before the drop; 0 drops it before any byte |
| `stall` | `true` never answers |
| `garbage_rate` | probability of a malformed answer |

`CHAOS_PROFILES=profiles.json` replaces the built-in profiles with the list in
that file. The profiles can also change while the server runs:

| Endpoint | Role |
|---|---|
| `GET /admin/profiles` | list the profiles |
| `PUT /admin/profiles/{name}` | create or replace a profile; the name comes from the path |
| `DELETE /admin/profiles/{name}` | remove a profile |

```bash
curl -X PUT localhost:9090/admin/profiles/fast \
  -d '{"ttft":{"median_ms":20},"completion_tokens":{"min":40,"max":120},"error":{"rate":0.5,"statuses":[503]}}'
```

This degrades a model in the middle of a test without a restart.

## Configuration

| Variable | Default | Effect |
|---|---|---|
| `CHAOS_ADDR` | `:9090` | listen address |
| `CHAOS_PROFILES` | built-in profiles | JSON file with the list of profiles |
| `CHAOS_SEED` | current time | seed of the random generator |
| `CHAOS_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

With the same `CHAOS_SEED` and the same requests in the same order, the server
draws the same delays and faults.

## Monitoring

`GET /metrics` serves Prometheus metrics:

- `chaos_requests_total{profile, outcome}`, where `outcome` is `ok`,
  `error`, `hangup`, `stall`, `garbage`, `cancelled` (the client left first)
  or `unknown_model`;
- `chaos_request_duration_seconds{profile}`, a histogram of the time spent on
  each request, injected delays included.

Comparing them with what the client saw tells whether every fault reached
it, and in what form. `GET /healthz` answers `ok`.

## Security

chaos-provider has no authentication, and its admin endpoints change its
behaviour. Run it on a test network only, never exposed to the internet.

## License

Apache License 2.0.
