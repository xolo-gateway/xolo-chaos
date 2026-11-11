-- Completes the dataset produced by cmd/seed with what the campaign needs.
-- Written for both SQLite and PostgreSQL: delete then insert rather than a
-- SQLite-specific INSERT OR REPLACE, and true/false boolean literals that both
-- engines accept.
--
-- @@PROVIDER_URL@@, @@RATE_LIMIT@@ and @@RETRY@@ are substituted by the Makefile.

-- A dedicated provider, rather than rewiring prov-acme-openai: the dataset's
-- provider carries a 1 req/s limit and three retry attempts, which would cap
-- the campaign and hide the injected faults. Its encrypted key is copied as is,
-- so the same XOLO_SECRET_KEY decrypts it.
DELETE FROM llm_models WHERE provider_id = 'prov-chaos';
DELETE FROM providers WHERE id = 'prov-chaos';

INSERT INTO providers
  (id, created_at, updated_at, org_id, name, type, base_url, api_key, active,
   currency, cloud_tier, retry_config, rate_limit_config, billing_mode, subscription_plan)
SELECT 'prov-chaos', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'org-acme',
       'Chaos provider', 'openai', '@@PROVIDER_URL@@', api_key, 1,
       'EUR', 0, '@@RETRY@@', '@@RATE_LIMIT@@', 'payg', NULL
  FROM providers WHERE id = 'prov-acme-openai';

DELETE FROM llm_models WHERE id LIKE 'mdl-chaos-%';
INSERT INTO llm_models
  (id, created_at, updated_at, provider_id, org_id, proxy_name, real_model, description, enabled,
   prompt_cost_per1_k_tokens, completion_cost_per1_k_tokens,
   context_window, output_window, active_params, tokens_per_sec_low, tokens_per_sec_high)
VALUES
  ('mdl-chaos-fast',    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-fast',    'chaos-fast',    'Baseline: immediate response.',                   1, 100, 200, 32000, 4096, 8000000000, 200, 400),
  ('mdl-chaos-slow',    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-slow',    'chaos-slow',    'TTFT long, 30 tokens/s.',                         1, 100, 200, 32000, 4096, 8000000000, 20, 40),
  ('mdl-chaos-flaky',   CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-flaky',   'chaos-flaky',   '15% of 429/503 with Retry-After.',                1, 100, 200, 32000, 4096, 8000000000, 200, 400),
  ('mdl-chaos-hangup',  CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-hangup',  'chaos-hangup',  'One response in three cut mid-stream.',           1, 100, 200, 32000, 4096, 8000000000, 60, 100),
  ('mdl-chaos-stall',   CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-stall',   'chaos-stall',   'Never answers.',                                  1, 100, 200, 32000, 4096, 8000000000, 0, 0),
  ('mdl-chaos-garbage', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-garbage', 'chaos-garbage', 'One response in four malformed.',                 1, 100, 200, 32000, 4096, 8000000000, 80, 120),
  ('mdl-chaos-mixed',   CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-mixed',   'chaos-mixed',   'A bit of everything.',                            1, 100, 200, 32000, 4096, 8000000000, 40, 80),
  ('mdl-chaos-monkey',  CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme', 'chaos-monkey',  'chaos-fast',    'fast profile behind the chaos-monkey middleware.',1, 100, 200, 32000, 4096, 8000000000, 200, 400);

-- The dataset's quotas are calibrated to show ~30% consumption over its
-- history: under load they are exhausted within seconds. Loosened here so that
-- the campaign measures the gateway, not the budget. Tighten them to test quota
-- enforcement.
UPDATE quota
   SET daily_budget   = COALESCE(daily_budget, 0)   * 100000,
       monthly_budget = COALESCE(monthly_budget, 0) * 100000,
       yearly_budget  = COALESCE(yearly_budget, 0)  * 100000,
       updated_at = CURRENT_TIMESTAMP
 WHERE scope_id IN ('org-acme', 'usr-alice', 'usr-carol', 'usr-dave', 'app-acme-ci');

-- Token for Prometheus. Xolo's /metrics endpoint requires the platform admin
-- role, which usr-root already holds; it only lacked a token. The plaintext
-- value is "xolo-chaos-metrics", stored as SHA-256 like any token created by
-- the application.
DELETE FROM auth_tokens WHERE id = 'tok-chaos-metrics';
INSERT INTO auth_tokens (id, created_at, updated_at, owner_id, application_id, label, value, org_id, expires_at)
VALUES ('tok-chaos-metrics', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'usr-root', NULL,
        'Prometheus (load test bench)', '950de209baad9646a7b78dcd91f70b79bcc76ffbe0609f675e8e72be47b0d9e6', '', NULL);

-- Middleware: generator -> chaos-monkey -> model (passthrough) -> sink,
-- applied to the chaos-monkey model only.
DELETE FROM middlewares WHERE id = 'mw-chaos-monkey';
INSERT INTO middlewares
  (id, org_id, name, description, enabled, priority, applies_to_all, targets_json, graph_json, created_at, updated_at)
VALUES (
  'mw-chaos-monkey', 'org-acme', 'chaos-monkey',
  'Faults injected into the plugin subsystem (load campaign).',
  true, 50, false,
  '[{"kind":"llm","id":"mdl-chaos-monkey"}]',
  '{"nodes":[
      {"id":"gen","type":"generator","position":{"x":0,"y":120}},
      {"id":"monkey","type":"plugin","position":{"x":280,"y":120},
       "data":{"pluginName":"chaos-monkey",
               "config":{"latency":{"median_ms":50,"sigma":0.8,"max_ms":2000},"reject_rate":0.02,"error_rate":0.02,"crash_rate":0.001}}},
      {"id":"llm","type":"model","position":{"x":560,"y":120},"data":{"passthrough":true}},
      {"id":"out","type":"sink","position":{"x":840,"y":120}}],
    "edges":[
      {"id":"e1","source":"gen","sourcePort":"request","target":"monkey","targetPort":"request"},
      {"id":"e2","source":"monkey","sourcePort":"request","target":"llm","targetPort":"request"},
      {"id":"e3","source":"llm","sourcePort":"response","target":"out","targetPort":"response"}]}',
  CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);
