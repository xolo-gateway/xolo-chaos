package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	proto "github.com/xolo-gateway/xolo/pkg/pluginsdk/proto"

	"github.com/xolo-gateway/xolo-chaos/internal/chaos"
)

const PluginName = "chaos-monkey"
const PluginVersion = "0.1.0"

// Plugin injects faults in the plugin subsystem itself: latency in the
// pipeline, rejections, gRPC errors and process crashes. It is the only kind
// of chaos a plugin can produce; upstream faults belong to chaos-provider.
type Plugin struct {
	proto.UnimplementedXoloPluginServer

	rand *chaos.Rand
	// exit is os.Exit in production and a recorder in tests.
	exit func(code int)
	// sleep is the interruptible wait; swapped in tests to keep them fast.
	sleep func(ctx context.Context, d time.Duration) bool
}

// NewPlugin builds a plugin with the given seed.
func NewPlugin(seed int64) *Plugin {
	return &Plugin{rand: chaos.NewRand(seed), exit: os.Exit, sleep: wait}
}

func (p *Plugin) Describe(_ context.Context, _ *proto.DescribeRequest) (*proto.PluginDescriptor, error) {
	return &proto.PluginDescriptor{
		Name:         PluginName,
		Version:      PluginVersion,
		Description:  "Injects faults into the pipeline: latency, rejections, errors and plugin crashes. For load testing only.",
		Capabilities: []proto.PluginDescriptor_Capability{proto.PluginDescriptor_PRE_REQUEST},
		ConfigSchema: configSchemaJSON,
		InputPorts:   []*proto.PortDescriptor{{Name: "request", PortType: "request", Required: true}},
		OutputPorts:  []*proto.PortDescriptor{{Name: "request", PortType: "request"}},
	}, nil
}

func (p *Plugin) PreRequest(ctx context.Context, in *proto.PreRequestInput) (*proto.PreRequestOutput, error) {
	cfg, err := ParseConfig(in.GetCtx().GetConfigJson())
	if err != nil {
		slog.WarnContext(ctx, "chaos-monkey: invalid config, passing through", slog.Any("error", err))
		return &proto.PreRequestOutput{Allowed: true}, nil
	}

	// Crash first: a crashing plugin does not get to be polite about it.
	if p.rand.Hit(cfg.CrashRate) {
		slog.WarnContext(ctx, "chaos-monkey: crashing on purpose")
		p.exit(2)
		// Only reached when exit is a test double.
		return nil, fmt.Errorf("chaos-monkey: process would have exited")
	}

	if d := cfg.Latency.Sample(p.rand); d > 0 {
		if !p.sleep(ctx, d) {
			return nil, ctx.Err()
		}
	}

	if p.rand.Hit(cfg.ErrorRate) {
		return nil, fmt.Errorf("chaos-monkey: injected plugin error")
	}

	if p.rand.Hit(cfg.RejectRate) {
		return &proto.PreRequestOutput{
			Allowed:         false,
			RejectionReason: "chaos-monkey: injected rejection",
		}, nil
	}

	return &proto.PreRequestOutput{Allowed: true}, nil
}

func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
