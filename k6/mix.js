// Chaos mix: every profile at once at modest rates, for a long soak. The
// point is not throughput but behaviour under faults: are quotas still
// counted on truncated streams, does a stalled upstream leak goroutines or
// connections, does a crashed plugin come back, do 429s reach the client
// with their Retry-After.
//
// Read alongside Xolo's /metrics (goroutines, open connections, memory)
// scraped by the Prometheus of deploy/docker-compose.yml.
//
//   k6 run k6/mix.js
//   k6 run -e DURATION=1h k6/mix.js
import { chat } from './lib/xolo.js';

const DURATION = __ENV.DURATION || '15m';

function scenario(exec, rate, extra = {}) {
  return Object.assign({
    executor: 'constant-arrival-rate',
    rate, timeUnit: '1s', duration: DURATION,
    preAllocatedVUs: rate * 4, maxVUs: rate * 30,
    exec,
  }, extra);
}

export const options = {
  scenarios: {
    fast:    scenario('fast', 10),
    slow:    scenario('slow', 5),
    flaky:   scenario('flaky', 5),
    hangup:  scenario('hangup', 3),
    garbage: scenario('garbage', 2),
    monkey:  scenario('monkey', 5),
    // One stalled request every 10 s, each held up to its 30 s timeout:
    // about three connections permanently parked upstream.
    stall:   scenario('stall', 6, { timeUnit: '1m', preAllocatedVUs: 5, maxVUs: 20 }),
  },
  thresholds: {
    // The healthy lane must stay healthy whatever the others do.
    'llm_ttft{model:chaos-fast}': ['p(95)<400'],
    'checks{model:chaos-fast}': ['rate>0.999'],
    // Faults must reach the client as faults, not as hangs. An upstream cut
    // gives a truncated stream under a 200, not a transport error: the
    // headers have already been sent.
    'llm_rate_limited{model:chaos-flaky}': ['rate>0.02'],
    'llm_truncated_streams{model:chaos-hangup}': ['rate>0.1'],
    'llm_truncated_streams{model:chaos-fast}': ['rate<0.001'],
    'llm_duration{model:chaos-stall}': ['p(50)>25000'],
    // The plugin lane: rejections are 403s (not failures), crashes must not
    // turn into a lasting outage.
    'checks{model:chaos-monkey}': ['rate>0.9'],
  },
};

export function fast()    { chat('chaos-fast'); }
export function slow()    { chat('chaos-slow', { timeout: '90s' }); }
export function flaky()   { chat('chaos-flaky'); }
export function hangup()  { chat('chaos-hangup'); }
export function garbage() { chat('chaos-garbage'); }
export function monkey()  { chat('chaos-monkey'); }
export function stall()   { chat('chaos-stall', { timeout: '30s' }); }
