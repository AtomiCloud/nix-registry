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

// Review of PR 125, thread 5: unit variants come from VictoriaMetrics' unit table, hertz and the rest included.
func TestPlatformStoredNamesCoverTheUnitWords(t *testing.T) {
	has := func(key, want string) {
		t.Helper()
		if !in(platformStored(key), want) {
			t.Errorf("%s: no %s", key, want)
		}
	}
	has("system.cpu.frequency", "system_cpu_frequency_hertz")
	has("system.cpu.frequency", "system_cpu_frequency_hertz_bucket")
	has("system.cpu.temperature", "system_cpu_temperature_celsius")
	has("system.memory.utilization", "system_memory_utilization_percent")
	has("process.disk.io", "process_disk_io_kibibytes_total")
	has("process.uptime", "process_uptime_hours")
	has("http.server.request.duration", "http_server_request_duration_microseconds_sum")
}

// Review of PR 125, thread 6: a counter key ending in total, with a unit, is stored with the unit
// before the moved total; the unitless counter keeps the key.
func TestPlatformCounterTotalKeyWithUnit(t *testing.T) {
	keep := platformStored("requests_total")
	for _, want := range []string{"requests_total", "requests_seconds_total", "requests_bytes_total", "requests_hertz_total"} {
		if !in(keep, want) {
			t.Errorf("requests_total: no %s in %v", want, keep)
		}
	}
	dotted := platformStored("requests.total")
	for _, want := range []string{"requests_total", "requests_seconds_total"} {
		if !in(dotted, want) {
			t.Errorf("requests.total: no %s in %v", want, dotted)
		}
	}
	c := parse(t, "service: s\nplatform_metrics:\n  requests_total: [x]\n")
	if !in(c.KeepNames(), "requests_seconds_total") {
		t.Errorf("keep list lacks requests_seconds_total: %v", c.KeepNames())
	}
	if _, ok := c.Lookup("requests_seconds_total"); !ok {
		t.Error("lookup does not resolve requests_seconds_total")
	}
}
