// Package storemove encodes concept section 10.6 as a fixed, fail-closed sequence.
// It never changes app configuration, Alloy configuration or stable Services.
package storemove

import (
	"context"
	"fmt"
	"io"
	"time"
)

type Step string

const (
	Prepare          Step = "apply scaled form and wait for every part Ready"
	Route            Step = "flip role labels to scaled pods"
	Reconnect        Step = "roll Alloy (at least two replicas); require old received counter flat"
	Drain            Step = "stop old compaction; require completed and flushed blocks; stop old single"
	PyroTransition   Step = "no drain; retain original StatefulSet as Pyroscope v2 metastore"
	Verify           Step = "read known data through read Service; compare 60 per-minute counts"
	AddNodes         Step = "start new storage nodes and wait Ready"
	StorageRoute     Step = "add storageNode endpoints and roll insert/select"
	Writes           Step = "require received counter rising and send errors flat (no drain)"
	RestoreRoute     Step = "remove new storageNode endpoints and roll insert/select; retain new PVCs and pods"
	Restore          Step = "restore single with role templates active before scale-up, wait Ready and undo drain controls"
	ReverseRoles     Step = "confirm single pod roles, then clear scaled pod and template roles"
	ReverseReconnect Step = "roll Alloy and require every scaled received counter flat"
)

type Config struct {
	Context                 string              `yaml:"context"`
	Namespace               string              `yaml:"namespace"`
	ScaledManifest          string              `yaml:"scaled_manifest"`
	SingleManifest          string              `yaml:"single_manifest"`
	MetastoreManifest       string              `yaml:"metastore_manifest"`
	RoutingManifest         string              `yaml:"routing_manifest"`
	OriginalRoutingManifest string              `yaml:"original_routing_manifest"`
	Scaled                  []string            `yaml:"scaled"`
	Routers                 []string            `yaml:"routers"`
	Single                  string              `yaml:"single"`
	Alloy                   string              `yaml:"alloy"`
	AlloyConfigObjects      []string            `yaml:"alloy_config_objects"`
	ScaledMetricsURLs       []string            `yaml:"scaled_metrics_urls"`
	RoleLabels              []string            `yaml:"role_labels"`
	RoleWorkloads           map[string][]string `yaml:"role_workloads"`
	// A separate backend-worker can be scaled to zero. For target=all a verified
	// in-pod control adapter is required; no unimplemented Tempo URL is guessed.
	Compactor         string   `yaml:"compactor"`
	CompactorReplicas int      `yaml:"compactor_replicas"`
	StopCompaction    []string `yaml:"stop_compaction"`
	ResumeCompaction  []string `yaml:"resume_compaction"`
	MetricsURL        string   `yaml:"metrics_url"`
	SingleMetricsURL  string   `yaml:"single_metrics_url"`
	ReadURL           string   `yaml:"read_url"`
	Query             string   `yaml:"query"`
	ProfileType       string   `yaml:"profile_type"`
	Tenant            string   `yaml:"tenant"`
}

func (c Config) Validate(store string, rollback bool) error {
	c = c.normalized()
	switch store {
	case "tempo", "pyroscope", "victorialogs", "victoriametrics":
	default:
		return fmt.Errorf("store=%q: want tempo, pyroscope, victorialogs or victoriametrics", store)
	}
	required := map[string]string{"context": c.Context, "namespace": c.Namespace, "scaled_manifest": c.ScaledManifest, "read_url": c.ReadURL, "query": c.Query}
	if len(c.Scaled) == 0 {
		return fmt.Errorf("scaled: list every new workload to wait for")
	}
	if storage(store) {
		required["routing_manifest"] = c.RoutingManifest
		required["original_routing_manifest"] = c.OriginalRoutingManifest
		required["metrics_url"] = c.MetricsURL
		if len(c.Routers) == 0 {
			return fmt.Errorf("routers: list insert and select workloads")
		}
	} else {
		required["single"] = c.Single
		required["alloy"] = c.Alloy
		required["single_metrics_url"] = c.SingleMetricsURL
		if len(c.RoleLabels) == 0 {
			return fmt.Errorf("role_labels: list every stable Service role")
		}
		covered := map[string]bool{}
		for workload, labels := range c.RoleWorkloads {
			found := false
			for _, w := range c.Scaled {
				if w == workload {
					found = true
				}
			}
			if !found || len(labels) == 0 {
				return fmt.Errorf("role_workloads %q must name a scaled workload and its roles", workload)
			}
			for _, label := range labels {
				allowed := false
				for _, role := range c.RoleLabels {
					if role == label {
						allowed = true
					}
				}
				if !allowed {
					return fmt.Errorf("role_workloads %s: undeclared role %q", workload, label)
				}
				covered[label] = true
			}
		}
		for _, label := range c.RoleLabels {
			if !covered[label] {
				return fmt.Errorf("role_workloads: no workload supplies role %q", label)
			}
		}
		for _, label := range c.RoleLabels {
			prefix := "obs.diene/" + store + "-"
			if len(label) <= len(prefix) || label[:len(prefix)] != prefix {
				return fmt.Errorf("role_labels: %q must start with %s", label, prefix)
			}
		}
		for _, w := range c.Scaled {
			if w == c.Single {
				return fmt.Errorf("scaled contains the old single %q", w)
			}
		}
		if store == "tempo" {
			if c.Compactor != "" && c.CompactorReplicas < 1 {
				return fmt.Errorf("compactor_replicas: supply the original positive replica count for rollback")
			}
			if c.Compactor == c.Single {
				return fmt.Errorf("compactor=%q: refusing to stop undrained single; provide separate worker or in-pod control adapter", c.Compactor)
			}
			if c.Compactor == "" && (len(c.StopCompaction) == 0 || len(c.ResumeCompaction) == 0) {
				return fmt.Errorf("tempo requires separate compactor or verified stop_compaction and resume_compaction argv; no public stop-compaction endpoint is assumed")
			}
		}
		if rollback {
			required["single_manifest"] = c.SingleManifest
			if len(c.ScaledMetricsURLs) == 0 {
				required["metrics_url (scaled receive tier for rollback)"] = c.MetricsURL
			}
		}
	}
	if store == "pyroscope" {
		required["profile_type"] = c.ProfileType
		required["metastore_manifest"] = c.MetastoreManifest
	}
	for k, v := range required {
		if v == "" {
			return fmt.Errorf("%s is required", k)
		}
	}
	return nil
}
func storage(s string) bool { return s == "victorialogs" || s == "victoriametrics" }
func Plan(store string, rollback bool) []Step {
	if storage(store) {
		if rollback {
			return []Step{RestoreRoute}
		}
		return []Step{AddNodes, StorageRoute, Writes, Verify}
	}
	if rollback {
		return []Step{Restore, ReverseRoles, ReverseReconnect, Verify}
	}
	if store == "pyroscope" {
		return []Step{Prepare, Route, Reconnect, PyroTransition, Verify}
	}
	return []Step{Prepare, Route, Reconnect, Drain, Verify}
}

type Cluster interface {
	Execute(context.Context, Step, string, Config) error
}
type Metrics interface {
	Counter(context.Context, string, string) (float64, error)
	Counts(context.Context, string, Config, time.Time) ([]float64, error)
}
type Runner struct {
	Cluster  Cluster
	Metrics  Metrics
	Out      io.Writer
	Interval time.Duration
	// Pause and Now are injectable; tests never sleep or contact a cluster.
	Pause func(context.Context, time.Duration) error
	Now   func() time.Time
}

func (r Runner) pause(ctx context.Context) error {
	if r.Pause != nil {
		return r.Pause(ctx, r.Interval)
	}
	d := r.Interval
	if d == 0 {
		d = 15 * time.Second
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func received(store string) string {
	switch store {
	case "tempo":
		return "tempo_distributor_spans_received_total"
	case "pyroscope":
		return "pyroscope_distributor_received_compressed_bytes_count"
	case "victorialogs":
		return `vl_rows_ingested_total{type="opentelemetry_protobuf"}`
	default:
		return "vm_rows_inserted_total"
	}
}
func (r Runner) counterPair(ctx context.Context, endpoint, name string, rising bool) error {
	a, err := r.Metrics.Counter(ctx, endpoint, name)
	if err != nil {
		return err
	}
	if err = r.pause(ctx); err != nil {
		return err
	}
	b, err := r.Metrics.Counter(ctx, endpoint, name)
	if err != nil {
		return err
	}
	if rising && b <= a {
		return fmt.Errorf("%s: counter must rise, got %g -> %g", name, a, b)
	}
	if !rising && b != a {
		return fmt.Errorf("%s: counter must stay flat, got %g -> %g (reset is not proof)", name, a, b)
	}
	return nil
}
func compare(a, b []float64) error {
	if len(a) != 60 || len(b) != 60 {
		return fmt.Errorf("known dataset: want exactly 60 minute counts, got %d and %d", len(a), len(b))
	}
	nonzero := false
	for i, v := range a {
		if v > 0 {
			nonzero = true
		}
		if v != b[i] {
			return fmt.Errorf("known dataset minute %d: count %g -> %g", i, v, b[i])
		}
	}
	if !nonzero {
		return fmt.Errorf("known dataset: all 60 baseline minutes are zero")
	}
	return nil
}
func (r Runner) Run(ctx context.Context, store string, c Config, rollback, dry bool) error {
	c = c.normalized()
	if err := c.Validate(store, rollback); err != nil {
		return err
	}
	out := r.Out
	if out == nil {
		out = io.Discard
	}
	plan := Plan(store, rollback)
	if dry {
		for i, s := range plan {
			fmt.Fprintf(out, "%d. %s\n", i+1, s)
		}
		return nil
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	end := now().UTC().Truncate(time.Minute).Add(-time.Minute)
	before, err := r.Metrics.Counts(ctx, store, c, end)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	if err = compare(before, before); err != nil {
		return err
	}
	for i, s := range plan {
		fmt.Fprintf(out, "%d. %s\n", i+1, s)
		switch s {
		case Reconnect:
			if err = r.Cluster.Execute(ctx, s, store, c); err == nil {
				err = r.counterPair(ctx, c.SingleMetricsURL, received(store), false)
			}
		case Drain, PyroTransition:
			if store == "tempo" {
				a, e := r.Metrics.Counter(ctx, c.SingleMetricsURL, "tempo_live_store_blocks_completed_total")
				if e != nil {
					return e
				}
				f, e := r.Metrics.Counter(ctx, c.SingleMetricsURL, "tempo_live_store_local_blocks_flushed_total")
				if e != nil {
					return e
				}
				if err = r.Cluster.Execute(ctx, Step("stop compaction"), store, c); err != nil {
					break
				}
				// Both transitions must happen after the baseline. Never stop on completed
				// alone: local completion does not establish object-store durability.
				for err == nil {
					if err = r.pause(ctx); err != nil {
						break
					}
					var b, g float64
					b, err = r.Metrics.Counter(ctx, c.SingleMetricsURL, "tempo_live_store_blocks_completed_total")
					if err != nil {
						break
					}
					g, err = r.Metrics.Counter(ctx, c.SingleMetricsURL, "tempo_live_store_local_blocks_flushed_total")
					if err != nil {
						break
					}
					if b < a || g < f {
						err = fmt.Errorf("Tempo drain counters reset")
						break
					}
					if b > a && g > f && g >= b {
						break
					}
				}
			}
			if err == nil {
				operation := Step("stop single")
				if store == "pyroscope" {
					operation = Step("retain single as metastore")
				}
				err = r.Cluster.Execute(ctx, operation, store, c)
			}
		case Writes:
			var errorsBefore float64
			if store == "victorialogs" {
				errorsBefore, err = r.Metrics.Counter(ctx, c.MetricsURL, "vl_insert_remote_send_errors_total")
			}
			if err == nil {
				err = r.counterPair(ctx, c.MetricsURL, received(store), true)
			}
			if err == nil && store == "victorialogs" {
				var after float64
				after, err = r.Metrics.Counter(ctx, c.MetricsURL, "vl_insert_remote_send_errors_total")
				if err == nil && after != errorsBefore {
					err = fmt.Errorf("vl_insert_remote_send_errors_total: must stay flat, got %g -> %g", errorsBefore, after)
				}
			}
		case Verify:
			var after []float64
			after, err = r.Metrics.Counts(ctx, store, c, end)
			if err == nil {
				err = compare(before, after)
			}
		case ReverseReconnect:
			err = r.Cluster.Execute(ctx, s, store, c)
			if err == nil {
				urls := c.ScaledMetricsURLs
				if len(urls) == 0 {
					urls = []string{c.MetricsURL}
				}
				for _, endpoint := range urls {
					if err = r.counterPair(ctx, endpoint, received(store), false); err != nil {
						break
					}
				}
			}
		default:
			err = r.Cluster.Execute(ctx, s, store, c)
		}
		if err != nil {
			return fmt.Errorf("step %d (%s): %w; stopped without skipping; inspect state before explicit rollback", i+1, s, err)
		}
	}
	if rollback && storage(store) {
		fmt.Fprintln(out, "new storage nodes and PVCs retained: new-node data is unreadable until endpoints are added back")
	}
	return nil
}
