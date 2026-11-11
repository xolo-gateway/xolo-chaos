package main

import (
	"encoding/json"

	"github.com/xolo-gateway/xolo-chaos/internal/chaos"
)

const configSchemaJSON = `{
  "type": "object",
  "properties": {
    "latency": {
      "type": "object",
      "title": "Latency injected into the pipeline",
      "description": "Log-normal delay added before handing control back to the engine. median_ms 0 disables it.",
      "properties": {
        "median_ms": {"type": "number", "title": "Median (ms)", "default": 0},
        "sigma": {"type": "number", "title": "Spread (sigma)", "default": 0.5},
        "max_ms": {"type": "number", "title": "Cap (ms)", "default": 0}
      }
    },
    "reject_rate": {
      "type": "number", "minimum": 0, "maximum": 1, "default": 0,
      "title": "Rejection rate",
      "description": "Probability of rejecting the request (403 on the client side)."
    },
    "error_rate": {
      "type": "number", "minimum": 0, "maximum": 1, "default": 0,
      "title": "gRPC error rate",
      "description": "Probability of answering the engine with an error, as a buggy plugin would."
    },
    "crash_rate": {
      "type": "number", "minimum": 0, "maximum": 1, "default": 0,
      "title": "Crash rate",
      "description": "Probability of terminating the plugin process without warning, to test supervision and restart by Xolo."
    }
  }
}`

// Config is the per-node configuration.
type Config struct {
	Latency    chaos.Latency `json:"latency"`
	RejectRate float64       `json:"reject_rate"`
	ErrorRate  float64       `json:"error_rate"`
	CrashRate  float64       `json:"crash_rate"`
}

// ParseConfig decodes the node configuration; an empty one is all zeros,
// which is a plugin that does nothing but cost a gRPC round trip.
func ParseConfig(raw string) (Config, error) {
	var cfg Config
	if raw == "" || raw == "{}" {
		return cfg, nil
	}
	err := json.Unmarshal([]byte(raw), &cfg)
	return cfg, err
}
