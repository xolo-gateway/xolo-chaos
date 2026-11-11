-- Realistic pipelines, to measure what they cost compared with the bare proxy.
-- Applied after prepare-db.sql by `make seed`. Written for both SQLite and
-- PostgreSQL.
--
-- Three models are added to the catalog:
--   acme/chaos-pseudo  a model wrapped by the pseudonymizer middleware
--   acme/chaos-guard   a virtual model gen -> prompt-guard -> model
--   acme/chaos-smart   a virtual model with complexity-based routing
--
-- All of them ultimately point to the chaos provider's `fast` profile, so the
-- measured cost difference comes from the pipeline and nothing else.

DELETE FROM llm_models WHERE id = 'mdl-chaos-pseudo';
INSERT INTO llm_models
  (id, created_at, updated_at, provider_id, org_id, proxy_name, real_model, description, enabled,
   prompt_cost_per1_k_tokens, completion_cost_per1_k_tokens,
   context_window, output_window, active_params, tokens_per_sec_low, tokens_per_sec_high)
VALUES
  ('mdl-chaos-pseudo', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'prov-chaos', 'org-acme',
   'chaos-pseudo', 'chaos-fast', 'fast profile behind the pseudonymizer middleware.',
   1, 100, 200, 32000, 4096, 8000000000, 200, 400);

-- Middleware: generator -> pseudonymizer -> model (passthrough) -> sink.
-- The plugin also runs on the way back to restore the names, so the request
-- crosses the gRPC boundary twice.
DELETE FROM middlewares WHERE id = 'mw-chaos-pseudo';
INSERT INTO middlewares
  (id, org_id, name, description, enabled, priority, applies_to_all, targets_json, graph_json, created_at, updated_at)
VALUES (
  'mw-chaos-pseudo', 'org-acme', 'chaos-pseudonymizer',
  'Pseudonymization of personal data (load test bench).',
  true, 20, false,
  '[{"kind":"llm","id":"mdl-chaos-pseudo"}]',
  '{"nodes":[
      {"id":"gen","type":"generator","position":{"x":0,"y":120}},
      {"id":"pseudo","type":"plugin","position":{"x":260,"y":120},
       "data":{"pluginName":"pseudonymizer","config":{"language":"fr","strategy":"tag"}}},
      {"id":"llm","type":"model","position":{"x":520,"y":120},"data":{"passthrough":true}},
      {"id":"out","type":"sink","position":{"x":780,"y":120}}],
    "edges":[
      {"id":"e1","source":"gen","sourcePort":"request","target":"pseudo","targetPort":"request"},
      {"id":"e2","source":"pseudo","sourcePort":"request","target":"llm","targetPort":"request"},
      {"id":"e3","source":"llm","sourcePort":"response","target":"out","targetPort":"response"}]}',
  CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);

-- Virtual model: generator -> prompt-guard -> model -> sink.
DELETE FROM virtual_models WHERE id = 'vm-chaos-guard';
INSERT INTO virtual_models (id, org_id, name, description, graph_json, created_at, updated_at)
VALUES (
  'vm-chaos-guard', 'org-acme', 'chaos-guard',
  'Prompt injection guard in front of the fast profile.',
  '{"nodes":[
      {"id":"gen","type":"generator","position":{"x":0,"y":120}},
      {"id":"guard","type":"plugin","position":{"x":260,"y":120},
       "data":{"pluginName":"prompt-guard","config":{"block_above":0.7,"event_above":0.4}}},
      {"id":"llm","type":"model","position":{"x":520,"y":120},"data":{"proxyName":"chaos-fast"}},
      {"id":"out","type":"sink","position":{"x":780,"y":120}}],
    "edges":[
      {"id":"e1","source":"gen","sourcePort":"request","target":"guard","targetPort":"request"},
      {"id":"e2","source":"guard","sourcePort":"request","target":"llm","targetPort":"request"},
      {"id":"e3","source":"llm","sourcePort":"response","target":"out","targetPort":"response"}]}',
  CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);

-- Virtual model: complexity-based routing. The plugin's score goes through a
-- comparator, which picks one of two models through a select. Six nodes are
-- traversed before the upstream call, only one of them a plugin.
DELETE FROM virtual_models WHERE id = 'vm-chaos-smart';
INSERT INTO virtual_models (id, org_id, name, description, graph_json, created_at, updated_at)
VALUES (
  'vm-chaos-smart', 'org-acme', 'chaos-smart',
  'Complexity-based routing between a fast model and a heavy model.',
  '{"nodes":[
      {"id":"gen","type":"generator","position":{"x":0,"y":160}},
      {"id":"scorer","type":"plugin","position":{"x":240,"y":60},
       "data":{"pluginName":"complexity-scorer","config":{}}},
      {"id":"cmp","type":"compare","position":{"x":470,"y":60},"data":{"op":"gt","threshold":0.5}},
      {"id":"strong","type":"model_ref","position":{"x":470,"y":180},"data":{"proxyName":"chaos-slow"}},
      {"id":"fast","type":"model_ref","position":{"x":470,"y":260},"data":{"proxyName":"chaos-fast"}},
      {"id":"sel","type":"select","position":{"x":700,"y":160}},
      {"id":"llm","type":"model","position":{"x":930,"y":160},"data":{}},
      {"id":"out","type":"sink","position":{"x":1160,"y":160}}],
    "edges":[
      {"id":"e1","source":"gen","sourcePort":"request","target":"scorer","targetPort":"request"},
      {"id":"e2","source":"scorer","sourcePort":"complexity","target":"cmp","targetPort":"value"},
      {"id":"e3","source":"cmp","sourcePort":"result","target":"sel","targetPort":"condition"},
      {"id":"e4","source":"strong","sourcePort":"model_name","target":"sel","targetPort":"when_true"},
      {"id":"e5","source":"fast","sourcePort":"model_name","target":"sel","targetPort":"when_false"},
      {"id":"e6","source":"sel","sourcePort":"value","target":"llm","targetPort":"model_name"},
      {"id":"e7","source":"gen","sourcePort":"request","target":"llm","targetPort":"request"},
      {"id":"e8","source":"llm","sourcePort":"response","target":"out","targetPort":"response"}]}',
  CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);
