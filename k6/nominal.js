// Nominal plateau: a steady open-loop arrival rate on the healthy profile,
// streaming and non-streaming. Establishes the baseline the other campaigns
// are read against: p95 TTFT, p99 duration, zero errors.
//
//   k6 run k6/nominal.js
//   k6 run -e RATE=50 -e DURATION=10m k6/nominal.js
import { chat, listModels } from './lib/xolo.js';
import { check } from 'k6';

const RATE = Number(__ENV.RATE || 20);
const DURATION = __ENV.DURATION || '5m';

export const options = {
  scenarios: {
    streaming: {
      executor: 'constant-arrival-rate',
      rate: RATE, timeUnit: '1s', duration: DURATION,
      preAllocatedVUs: RATE * 2, maxVUs: RATE * 10,
      exec: 'streaming',
    },
    blocking: {
      executor: 'constant-arrival-rate',
      rate: Math.max(1, Math.round(RATE / 4)), timeUnit: '1s', duration: DURATION,
      preAllocatedVUs: RATE, maxVUs: RATE * 5,
      exec: 'blocking',
    },
  },
  thresholds: {
    'checks{model:chaos-fast}': ['rate>0.999'],
    'llm_ttft{model:chaos-fast}': ['p(95)<300'],
    'llm_duration{model:chaos-fast}': ['p(99)<1500'],
    llm_upstream_errors: ['rate<0.001'],
    llm_transport_errors: ['rate<0.001'],
    dropped_iterations: ['count<10'],
  },
};

export function setup() {
  const res = listModels();
  check(res, { 'models endpoint answers': (r) => r.status === 200 });
  if (res.status !== 200) throw new Error(`Xolo not ready: ${res.status} ${res.body}`);
}

export function streaming() { chat('chaos-fast', { stream: true }); }
export function blocking() { chat('chaos-fast', { stream: false }); }
