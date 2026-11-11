// Shared helpers for the Xolo campaigns.
//
// k6's http module does not read a body incrementally: for a streaming call
// `timings.waiting` (time to first byte) is the time to first token and
// `timings.duration` the full generation. Counting chunks or measuring
// inter-token gaps needs the xk6-sse extension; the campaigns below stay on
// the stock binary on purpose.
import http from 'k6/http';
import { check } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';

export const BASE_URL = __ENV.XOLO_URL || 'http://localhost:3002';
export const TOKEN = __ENV.XOLO_TOKEN || 'xolo-e2e-alice-acme';
export const ORG = __ENV.XOLO_ORG || 'acme';

export const ttft = new Trend('llm_ttft', true);
export const e2e = new Trend('llm_duration', true);
export const upstreamErrors = new Rate('llm_upstream_errors');   // 5xx
export const rateLimited = new Rate('llm_rate_limited');         // 429
export const transportErrors = new Rate('llm_transport_errors'); // connection lost, timeout
// A truncated stream is NOT a transport error: when the upstream cuts mid-stream,
// Xolo has already sent its headers, so it returns 200, then an error event,
// and closes cleanly. The only reliable client-side signal is the missing
// [DONE].
export const truncated = new Rate('llm_truncated_streams');
export const completionTokens = new Counter('llm_completion_tokens');

// The prompts are in French on purpose: the pseudonymizer under test targets
// French text. Both sets share the language so that the difference between
// them measures the entities to detect, not the language.
const PROMPTS = {
  // Neutral text: what a pipeline sees when it has nothing to do.
  plain: [
    'Bonjour, comment vas-tu ?',
    'Résume en trois phrases les enjeux du chiffrement de bout en bout.',
    'Écris une fonction Go qui inverse une chaîne de caractères en respectant les runes.',
    'Quelle est la différence entre un percentile et une moyenne ? Donne un exemple.',
    'Propose un plan de test de charge pour une passerelle HTTP.',
  ],
  // Personal data (French names, cities and firms), so that the pseudonymizer
  // has real work to do: with no names or places to detect, it returns
  // immediately and the measurement says nothing about the real cost of
  // pseudonymization.
  pii: [
    "Rédige un courrier à Madame Claire Beaumont, 14 rue des Lilas à Villeurbanne, pour l'informer que son dossier a été transféré à Julien Mercier du service juridique de Nantes.",
    "Marc Delaunay et Sophie Nguyen de l'agence de Bordeaux demandent un devis pour la société Valmorel Industries, à livrer avant vendredi à Toulouse.",
    "Compte rendu : présents Anne Rivière, Paul Kowalski et Thomas Bianchi. Le dossier Lemoine passe à l'équipe de Lille, avec copie à Nadia Benali.",
    "Peux-tu résumer l'échange entre Élodie Fauvel, basée à Strasbourg, et le cabinet Morvan & Associés de Rennes au sujet du contrat de Pierre Vasseur ?",
    "Prépare un message pour Karim Haddad, responsable du site de Grenoble, l'informant que Camille Roussel reprend le portefeuille de Lyon à compter du 1er octobre.",
  ],
};

// The PROMPT_SET environment variable picks the default set.
const DEFAULT_SET = __ENV.PROMPT_SET || 'plain';

export function prompt(set) {
  const list = PROMPTS[set || DEFAULT_SET] || PROMPTS.plain;
  return list[Math.floor(Math.random() * list.length)];
}

/**
 * chat sends one chat completion to Xolo and records the LLM metrics.
 * @param {string} model     proxy name without the org prefix, e.g. "chaos-fast"
 * @param {object} [opts]    { stream: bool, timeout: '60s', tags: {} }
 */
export function chat(model, opts = {}) {
  const stream = opts.stream !== undefined ? opts.stream : true;
  const body = JSON.stringify({
    model: `${ORG}/${model}`,
    stream,
    messages: [{ role: 'user', content: prompt(opts.promptSet) }],
  });
  const tags = Object.assign({ model, stream: String(stream) }, opts.tags || {});

  const res = http.post(`${BASE_URL}/api/v1/chat/completions`, body, {
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      'Content-Type': 'application/json',
      Accept: stream ? 'text/event-stream' : 'application/json',
    },
    tags,
    timeout: opts.timeout || '60s',
    // Every status is expected under chaos; failures are classified below
    // rather than through http_req_failed alone.
    responseCallback: http.expectedStatuses({ min: 200, max: 599 }),
  });

  ttft.add(res.timings.waiting, tags);
  e2e.add(res.timings.duration, tags);
  upstreamErrors.add(res.status >= 500, tags);
  rateLimited.add(res.status === 429, tags);
  transportErrors.add(res.status === 0, tags);

  if (res.status === 200) {
    const tokens = stream ? streamedCompletionTokens(res.body) : jsonCompletionTokens(res.body);
    if (tokens > 0) completionTokens.add(tokens, tags);
    if (stream) truncated.add(!res.body.includes('[DONE]'), tags);
  }

  check(res, {
    'status 200': (r) => r.status === 200,
    'body complete': (r) => r.status !== 200 || (stream ? r.body.includes('[DONE]') : bodyParses(r.body)),
  }, tags);

  return res;
}

function bodyParses(body) {
  try { JSON.parse(body); return true; } catch (_) { return false; }
}

function jsonCompletionTokens(body) {
  try { return JSON.parse(body).usage.completion_tokens || 0; } catch (_) { return 0; }
}

// The usage chunk is the last JSON event before [DONE].
function streamedCompletionTokens(body) {
  const idx = body.lastIndexOf('"usage"');
  if (idx < 0) return 0;
  const start = body.lastIndexOf('data: ', idx);
  const end = body.indexOf('\n', idx);
  try { return JSON.parse(body.slice(start + 6, end)).usage.completion_tokens || 0; } catch (_) { return 0; }
}

// Reads the list of models once per VU, useful as a readiness probe.
export function listModels() {
  return http.get(`${BASE_URL}/api/v1/models`, {
    headers: { Authorization: `Bearer ${TOKEN}` },
    tags: { name: 'models' },
  });
}
