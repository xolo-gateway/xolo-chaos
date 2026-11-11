package main

import (
	"context"
	"testing"
	"time"

	proto "github.com/xolo-gateway/xolo/pkg/pluginsdk/proto"
)

func newTestPlugin() (*Plugin, *int, *time.Duration) {
	p := NewPlugin(1)
	exited := -1
	var slept time.Duration
	p.exit = func(code int) { exited = code }
	p.sleep = func(_ context.Context, d time.Duration) bool { slept += d; return true }
	return p, &exited, &slept
}

func call(t *testing.T, p *Plugin, config string) (*proto.PreRequestOutput, error) {
	t.Helper()
	return p.PreRequest(context.Background(), &proto.PreRequestInput{
		Ctx:          &proto.RequestContext{ConfigJson: config},
		Model:        "acme/chaos-fast",
		MessagesJson: `[{"role":"user","content":"ping"}]`,
	})
}

func TestPassthroughByDefault(t *testing.T) {
	p, exited, slept := newTestPlugin()
	out, err := call(t, p, "")
	if err != nil || !out.Allowed || *exited != -1 || *slept != 0 {
		t.Fatalf("empty config must pass through: out=%v err=%v exited=%d slept=%s", out, err, *exited, *slept)
	}
}

func TestLatency(t *testing.T) {
	p, _, slept := newTestPlugin()
	out, err := call(t, p, `{"latency":{"median_ms":250}}`)
	if err != nil || !out.Allowed {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if *slept != 250*time.Millisecond {
		t.Fatalf("slept %s", *slept)
	}
}

func TestReject(t *testing.T) {
	p, _, _ := newTestPlugin()
	out, err := call(t, p, `{"reject_rate":1}`)
	if err != nil || out.Allowed || out.RejectionReason == "" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestError(t *testing.T) {
	p, _, _ := newTestPlugin()
	if _, err := call(t, p, `{"error_rate":1}`); err == nil {
		t.Fatal("expected an injected error")
	}
}

func TestCrash(t *testing.T) {
	p, exited, _ := newTestPlugin()
	if _, err := call(t, p, `{"crash_rate":1}`); err == nil || *exited != 2 {
		t.Fatalf("expected exit(2), got exited=%d err=%v", *exited, err)
	}
}

func TestInvalidConfigPassesThrough(t *testing.T) {
	p, _, _ := newTestPlugin()
	out, err := call(t, p, `{"reject_rate":"oui"}`)
	if err != nil || !out.Allowed {
		t.Fatalf("invalid config should not block traffic: out=%v err=%v", out, err)
	}
}

func TestDescribe(t *testing.T) {
	p, _, _ := newTestPlugin()
	d, err := p.Describe(context.Background(), &proto.DescribeRequest{})
	if err != nil || d.Name != PluginName || len(d.Capabilities) != 1 || d.Capabilities[0] != proto.PluginDescriptor_PRE_REQUEST {
		t.Fatalf("descriptor %+v err=%v", d, err)
	}
}
