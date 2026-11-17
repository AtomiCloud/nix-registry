// Package sizing implements the planning arithmetic in concept sections 2, 10.4
// and 10.7. These are estimates, not capacity guarantees.
package sizing

import (
	"fmt"
	"math"
)

type Input struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Services          int     `json:"services"`
	Pods              int     `json:"pods"`
	ProfiledPods      int     `json:"profiled_pods"`
}

type Estimate struct {
	Input                     Input   `json:"input"`
	TrafficCores              float64 `json:"traffic_cores"`
	TrafficMemoryGiB          float64 `json:"traffic_memory_gib"`
	PodIndexMiB               float64 `json:"pod_index_mib"`
	StoredSeries              int64   `json:"stored_series"`
	MetricsIngestMemoryGBLow  float64 `json:"metrics_ingest_memory_gb_low"`
	MetricsIngestMemoryGBHigh float64 `json:"metrics_ingest_memory_gb_high"`
	MetricsDiskGBPerDay       float64 `json:"metrics_disk_gb_per_day"`
	BucketGBPerDay            float64 `json:"bucket_gb_per_day"`
	LogsBucketGBPerDay        float64 `json:"logs_bucket_gb_per_day"`
	TracesBucketGBPerDay      float64 `json:"traces_bucket_gb_per_day"`
	ProfilesBucketGBPerDay    float64 `json:"profiles_bucket_gb_per_day"`
	ClassAPerDay              float64 `json:"class_a_per_day"`
	ClassBPerDay              float64 `json:"class_b_per_day"`
	Basis                     string  `json:"basis"`
}

func Calculate(in Input) (Estimate, error) {
	if math.IsNaN(in.RequestsPerSecond) || math.IsInf(in.RequestsPerSecond, 0) || in.RequestsPerSecond < 0 || in.RequestsPerSecond > 1e12 {
		return Estimate{}, fmt.Errorf("requests_per_second=%v: want finite 0..1e12", in.RequestsPerSecond)
	}
	if in.Services < 0 || in.Services > 1e9 || in.Pods < 0 || in.Pods > 1e9 || in.ProfiledPods < 0 || in.ProfiledPods > in.Pods {
		return Estimate{}, fmt.Errorf("services=%d pods=%d profiled_pods=%d: want nonnegative counts <=1e9 and profiled_pods <= pods", in.Services, in.Pods, in.ProfiledPods)
	}
	r := in.RequestsPerSecond / 1000
	series := int64(in.Services)*2520 + int64(in.Pods)*60
	return Estimate{Input: in, TrafficCores: .27 * r, TrafficMemoryGiB: .3 * r, PodIndexMiB: 110 * float64(in.Pods) / 1000, StoredSeries: series, MetricsIngestMemoryGBLow: float64(series) * .6 / 1e6, MetricsIngestMemoryGBHigh: float64(series) * 1.1 / 1e6, MetricsDiskGBPerDay: float64(series) * 5.3 / 1e6, BucketGBPerDay: 21*r + 4.7*float64(in.ProfiledPods)/30, LogsBucketGBPerDay: 16.6 * r, TracesBucketGBPerDay: 4 * r, ProfilesBucketGBPerDay: 4.7 * float64(in.ProfiledPods) / 30, ClassAPerDay: 17000 * r, ClassBPerDay: 28000 * r, Basis: "EXTRAPOLATED: concept 10.7 Loki-era logs (not VictoriaLogs); 8 spans and 3.2 log lines/request, tail sampling on. Traffic cores/memory are incremental; exclude idle base, query peaks, app and control-plane resources. Series: 2520/service + 60/pod; metrics ingest memory 0.6–1.1 KB/series, disk 5.3 GB/day/M series at 30s. Bucket total uses rounded 21 GB/day (components 16.6 + 4.0); VictoriaLogs has no bucket. Class A/B are traffic write-path estimates only; profile operations and read workload not measured."}, nil
}
