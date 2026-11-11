package main

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// metrics is a tiny Prometheus text exposition, enough to line the provider
// up against Xolo's own metrics without pulling the client library in.
type metrics struct {
	mu       sync.Mutex
	outcomes map[string]map[string]int64 // profile → outcome → count
	buckets  map[string][]int64          // profile → per-bound cumulative counts
	sums     map[string]float64
	counts   map[string]int64
}

var bounds = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

func newMetrics() *metrics {
	return &metrics{
		outcomes: map[string]map[string]int64{},
		buckets:  map[string][]int64{},
		sums:     map[string]float64{},
		counts:   map[string]int64{},
	}
}

func (m *metrics) inc(profile, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outcomes[profile] == nil {
		m.outcomes[profile] = map[string]int64{}
	}
	m.outcomes[profile][outcome]++
}

func (m *metrics) observe(profile string, d time.Duration) {
	s := d.Seconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.buckets[profile] == nil {
		m.buckets[profile] = make([]int64, len(bounds))
	}
	for i, b := range bounds {
		if s <= b {
			m.buckets[profile][i]++
		}
	}
	m.sums[profile] += s
	m.counts[profile]++
}

func (m *metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	fmt.Fprintln(w, "# HELP chaos_requests_total Requests served by the chaos provider, by profile and outcome.")
	fmt.Fprintln(w, "# TYPE chaos_requests_total counter")
	for _, profile := range sortedKeys(m.outcomes) {
		for _, outcome := range sortedKeys(m.outcomes[profile]) {
			fmt.Fprintf(w, "chaos_requests_total{profile=%q,outcome=%q} %d\n", profile, outcome, m.outcomes[profile][outcome])
		}
	}

	fmt.Fprintln(w, "# HELP chaos_request_duration_seconds Time spent serving a request, including injected delays.")
	fmt.Fprintln(w, "# TYPE chaos_request_duration_seconds histogram")
	for _, profile := range sortedKeys(m.buckets) {
		for i, b := range bounds {
			fmt.Fprintf(w, "chaos_request_duration_seconds_bucket{profile=%q,le=\"%g\"} %d\n", profile, b, m.buckets[profile][i])
		}
		fmt.Fprintf(w, "chaos_request_duration_seconds_bucket{profile=%q,le=\"+Inf\"} %d\n", profile, m.counts[profile])
		fmt.Fprintf(w, "chaos_request_duration_seconds_sum{profile=%q} %g\n", profile, m.sums[profile])
		fmt.Fprintf(w, "chaos_request_duration_seconds_count{profile=%q} %d\n", profile, m.counts[profile])
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
