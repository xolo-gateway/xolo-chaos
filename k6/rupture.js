// Breaking point: the arrival rate ramps up on the slow profile, whose long
// streams hold connections open, until Xolo stops keeping up. The test aborts
// as soon as errors cross 5 % so the last successful stage is the capacity.
//
//   k6 run k6/rupture.js
//   k6 run -e PEAK=500 -e MODEL=chaos-mixed k6/rupture.js
import { chat } from './lib/xolo.js';

const MODEL = __ENV.MODEL || 'chaos-slow';
const PEAK = Number(__ENV.PEAK || 200);

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: 5, timeUnit: '1s',
      preAllocatedVUs: 100, maxVUs: PEAK * 15,
      stages: [
        { duration: '1m', target: 5 },
        { duration: '2m', target: PEAK / 4 },
        { duration: '2m', target: PEAK / 2 },
        { duration: '2m', target: PEAK },
        { duration: '1m', target: PEAK },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: {
    // Breaching one of these stops the run: the current stage is the answer.
    http_req_failed: [{ threshold: 'rate<0.05', abortOnFail: true, delayAbortEval: '30s' }],
    llm_transport_errors: [{ threshold: 'rate<0.02', abortOnFail: true, delayAbortEval: '30s' }],
    // Informational: where the tail starts to degrade.
    llm_ttft: ['p(95)<3000'],
    llm_duration: ['p(99)<30000'],
    // Xolo should never be the one exhausting VUs.
    dropped_iterations: ['count<100'],
  },
};

export default function () {
  chat(MODEL, { stream: true, timeout: '90s' });
}
