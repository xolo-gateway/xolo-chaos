// Command chaos-monkey is a Xolo plugin that injects faults in the plugin
// subsystem: pipeline latency, rejections, gRPC errors and deliberate crashes.
// Drop the binary in XOLO_PLUGINS_DIR and place it in a middleware in front of
// the models under test.
//
// CHAOS_SEED, when set, makes the sequence of faults replayable.
package main

import (
	"os"
	"strconv"
	"time"

	"github.com/xolo-gateway/xolo/pkg/pluginsdk"
)

func main() {
	seed := time.Now().UnixNano()
	if v, err := strconv.ParseInt(os.Getenv("CHAOS_SEED"), 10, 64); err == nil {
		seed = v
	}
	pluginsdk.Serve(NewPlugin(seed))
}
