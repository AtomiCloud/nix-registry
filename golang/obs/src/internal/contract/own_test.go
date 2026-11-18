package contract

import (
	"strings"
	"testing"
)

// Platform keep names: unit variants for every key, the old-semconv millisecond duration and the ratio gauge included.
func TestPlatformStoredNames(t *testing.T) {
	has := func(key string, want ...string) {
		t.Helper()
		got := strings.Join(platformStored(key), ",")
		for _, w := range want {
			if !strings.Contains(","+got+",", ","+w+",") {
				t.Errorf("%s: no %s in %s", key, w, got)
			}
		}
	}
	has("process.cpu.utilization", "process_cpu_utilization_ratio", "process_cpu_utilization")
	has("http.server.duration", "http_server_duration_milliseconds_bucket", "http_server_duration_milliseconds_sum", "http_server_duration_milliseconds_count", "http_server_duration_milliseconds_total")
	has("http.server.request.duration", "http_server_request_duration_seconds_bucket", "http_server_request_duration_seconds_sum", "http_server_request_duration_seconds_count", "http_server_request_duration_milliseconds_bucket")
	has("process.runtime.memory", "process_runtime_memory_bytes", "process_runtime_memory_bytes_total")
	c := parse(t, "service: s\nplatform_metrics:\n  process.cpu.utilization: [x]\n  http.server.duration: [x]\n")
	got := c.KeepNames()
	for _, w := range []string{"process_cpu_utilization_ratio", "http_server_duration_milliseconds_bucket"} {
		if !in(got, w) {
			t.Errorf("keep list lacks %s: %v", w, got)
		}
	}
	for _, w := range []string{"process_cpu_utilization_ratio", "http_server_duration_milliseconds_count"} {
		if _, ok := c.Lookup(w); !ok {
			t.Errorf("lookup does not resolve %s", w)
		}
	}
}
