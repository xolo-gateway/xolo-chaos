// Single-profile campaign at a constant rate. Used to put a cost on a usage
// pattern: set MODEL, RATE and DURATION, then read the gateway's CPU and
// memory consumption alongside.
//
//   k6 run -e MODEL=chaos-slow -e RATE=10 -e DURATION=60s k6/profile.js
//   k6 run -e MODEL=chaos-fast -e STREAM=false k6/profile.js
import { chat } from './lib/xolo.js';

const MODEL = __ENV.MODEL || 'chaos-fast';
const RATE = Number(__ENV.RATE || 20);
const DURATION = __ENV.DURATION || '30s';
const STREAM = (__ENV.STREAM || 'true') === 'true';

export const options = {
  scenarios: {
    load: {
      executor: 'constant-arrival-rate',
      rate: RATE, timeUnit: '1s', duration: DURATION,
      // A long stream holds a VU for its whole duration: size generously,
      // otherwise k6 drops iterations and skews the measured rate.
      preAllocatedVUs: RATE * 10, maxVUs: RATE * 100,
    },
  },
  thresholds: {
    // No performance threshold: this script measures, it does not judge.
    dropped_iterations: ['count>=0'],
  },
};

export default function () {
  // PROMPT_SET=pii sends texts containing personal data, which the
  // pseudonymizer actually has to process.
  chat(MODEL, { stream: STREAM, timeout: '120s', promptSet: __ENV.PROMPT_SET });
}
