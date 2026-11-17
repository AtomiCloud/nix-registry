// Package storemove encodes concept section 10.6 as a fixed, fail-closed sequence.
// New objects are created (never applied over existing ones); every change to an
// existing object is a JSON patch obs builds from the live object, with test operations
// pinning the old values and a stored, verified inverse. It never changes app
// configuration, Alloy configuration or the stable Services.
package storemove

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

type Step string

const (
	Prepare          Step = "create the scaled form (new objects only) and wait for every part Ready"
	Route            Step = "flip role labels to scaled pods"
	Reconnect        Step = "roll Alloy (at least two replicas); require old received counter flat"
	Drain            Step = "stop old compaction; require completed and flushed blocks; stop old single"
	PyroTransition   Step = "no drain; convert the single to the Pyroscope v2 metastore in place"
	Verify           Step = "read known data through read Service; compare 60 per-minute counts"
	AddNodes         Step = "create the new storage nodes (new objects only) and wait Ready"
	StorageRoute     Step = "add the derived storage endpoints to -storageNode and roll insert/select"
	Writes           Step = "require received counter rising and send errors flat (no drain)"
	RestoreRoute     Step = "remove the added storage endpoints and roll insert/select; retain new PVCs and pods"
	Restore          Step = "restore a single obs stopped or converted (roles and replicas in one patch), wait Ready, undo drain controls"
	ReverseRoles     Step = "give the single's pods their roles, then clear scaled pod and template roles"
	ReverseReconnect Step = "roll Alloy and require every scaled received counter flat"

	// Cluster operations inside Drain/PyroTransition (Runner reads counters between them).
	StopCompaction  Step = "stop compaction"
	StopSingle      Step = "stop single"
	RetainMetastore Step = "retain single as metastore"
)

// Router is one VictoriaLogs/VictoriaMetrics insert or select workload and the storage
// port it dials (VictoriaMetrics: vminsert 8400, vmselect 8401; VictoriaLogs: 9491).
type Router struct {
	Workload string `yaml:"workload"`
	Role     string `yaml:"role"`
	Port     int    `yaml:"port"`
}

type Config struct {
	Context            string              `yaml:"context"`
	Namespace          string              `yaml:"namespace"`
	ScaledManifest     string              `yaml:"scaled_manifest"`
	Scaled             []string            `yaml:"scaled"`
	Routers            []Router            `yaml:"routers"`
	Storage            []string            `yaml:"storage_workloads"`
	Copies             int                 `yaml:"copies"`
	ClusterDomain      string              `yaml:"cluster_domain"`
	Single             string              `yaml:"single"`
	Alloy              string              `yaml:"alloy"`
	AlloyConfigObjects []string            `yaml:"alloy_config_objects"`
	ScaledMetricsURLs  []string            `yaml:"scaled_metrics_urls"`
	RoleLabels         []string            `yaml:"role_labels"`
	RoleWorkloads      map[string][]string `yaml:"role_workloads"`
	// A separate backend-worker can be scaled to zero (obs stores its replica count in
	// the inverse). For target=all a verified in-pod control adapter is required; no
	// unimplemented Tempo URL is guessed.
	Compactor        string   `yaml:"compactor"`
	StopCompaction   []string `yaml:"stop_compaction"`
	ResumeCompaction []string `yaml:"resume_compaction"`
	MetricsURL       string   `yaml:"metrics_url"`
	SingleMetricsURL string   `yaml:"single_metrics_url"`
	ReadURL          string   `yaml:"read_url"`
	Query            string   `yaml:"query"`
	ProfileType      string   `yaml:"profile_type"`
	Tenant           string   `yaml:"tenant"`
}

func (c Config) normalized() Config {
	c.Single = canonicalResource(c.Single)
	c.Alloy = canonicalResource(c.Alloy)
	c.Compactor = canonicalResource(c.Compactor)
	c.Scaled = append([]string(nil), c.Scaled...)
	c.Storage = append([]string(nil), c.Storage...)
	c.Routers = append([]Router(nil), c.Routers...)
	for i := range c.Scaled {
		c.Scaled[i] = canonicalResource(c.Scaled[i])
	}
	for i := range c.Storage {
		c.Storage[i] = canonicalResource(c.Storage[i])
	}
	for i := range c.Routers {
		c.Routers[i].Workload = canonicalResource(c.Routers[i].Workload)
	}
	roles := map[string][]string{}
	for w, labels := range c.RoleWorkloads {
		roles[canonicalResource(w)] = labels
	}
	c.RoleWorkloads = roles
	if c.ClusterDomain == "" {
		c.ClusterDomain = "cluster.local"
	}
	if c.Copies == 0 {
		c.Copies = 1
	}
	return c
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
	if c.Copies != 1 && c.Copies != 2 || c.Copies == 2 && store != "victoriametrics" {
		return fmt.Errorf("copies: %d; 1, or 2 for victoriametrics only (concept 10.5: two copies)", c.Copies)
	}
	if storage(store) {
		required["metrics_url"] = c.MetricsURL
		roles := map[string]int{}
		seen := map[string]bool{}
		for _, r := range c.Routers {
			if r.Role != "insert" && r.Role != "select" || r.Port < 1 || r.Port > 65535 || r.Workload == "" || seen[r.Workload] {
				return fmt.Errorf("routers: %+v: want a distinct workload, role insert or select and the storage port it dials", r)
			}
			seen[r.Workload] = true
			roles[r.Role]++
		}
		if roles["insert"] == 0 || roles["select"] == 0 {
			return fmt.Errorf("routers: list the insert and the select workloads")
		}
		if store != "victoriametrics" && len(c.Storage) > 0 {
			return fmt.Errorf("storage_workloads: only victoriametrics with copies: 2 changes the existing storage nodes (-dedup.minScrapeInterval=1ms)")
		}
		if c.Copies == 2 && len(c.Storage) == 0 || c.Copies == 1 && len(c.Storage) > 0 {
			return fmt.Errorf("storage_workloads: list the existing vmstorage workloads exactly when copies is 2 (they gain -dedup.minScrapeInterval=1ms)")
		}
		for _, f := range []struct {
			name string
			set  bool
		}{{"single", c.Single != ""}, {"alloy", c.Alloy != ""}, {"role_labels", len(c.RoleLabels) > 0}, {"role_workloads", len(c.RoleWorkloads) > 0}, {"compactor", c.Compactor != ""}} {
			if f.set {
				return fmt.Errorf("%s: not used by %s (it adds storage nodes; nothing is relabelled)", f.name, store)
			}
		}
	} else {
		if len(c.Routers) > 0 || len(c.Storage) > 0 {
			return fmt.Errorf("routers/storage_workloads: only victorialogs and victoriametrics add storage nodes")
		}
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
		if store == "pyroscope" && !strings.HasPrefix(c.Single, "statefulset/") {
			return fmt.Errorf("single=%q: the Pyroscope single is the StatefulSet that becomes the v2 metastore (same PVC, same raft id)", c.Single)
		}
		for _, w := range c.Scaled {
			if w == c.Single {
				return fmt.Errorf("scaled contains the old single %q", w)
			}
		}
		if store == "tempo" {
			if c.Compactor == c.Single {
				return fmt.Errorf("compactor=%q: refusing to stop undrained single; provide separate worker or in-pod control adapter", c.Compactor)
			}
			for _, w := range append([]string{c.Alloy}, c.Scaled...) {
				if c.Compactor != "" && c.Compactor == w {
					return fmt.Errorf("compactor=%q: is Alloy or a new scaled workload; name the separate backend worker", c.Compactor)
				}
			}
			if c.Compactor == "" && (len(c.StopCompaction) == 0 || len(c.ResumeCompaction) == 0) {
				return fmt.Errorf("tempo requires separate compactor or verified stop_compaction and resume_compaction argv; no public stop-compaction endpoint is assumed")
			}
		}
		if rollback {
			if len(c.ScaledMetricsURLs) == 0 {
				required["metrics_url (scaled receive tier for rollback)"] = c.MetricsURL
			}
		}
	}
	if store == "pyroscope" {
		required["profile_type"] = c.ProfileType
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

// Cluster runs the steps. Check is read-only: every static and live check before the
// first change, returning the plan index to start at (a re-run resumes from the live
// state; it never redoes an earlier step).
type Cluster interface {
	Check(ctx context.Context, store string, c Config, rollback bool) (int, error)
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
	// Static checks of everything obs will create, before anything else (dry run too).
	objs, err := validateScaledManifest(c, store)
	if err != nil {
		return err
	}
	if dry {
		for i, s := range plan {
			fmt.Fprintf(out, "%d. %s\n", i+1, s)
		}
		if storage(store) {
			nodes, _ := storageNodes(c, objs)
			for _, r := range c.Routers {
				fmt.Fprintf(out, "%s -storageNode += %v\n", r.Workload, endpoints(c, nodes, r.Port))
			}
		}
		return nil
	}
	start, err := r.Cluster.Check(ctx, store, c, rollback)
	if err != nil {
		return fmt.Errorf("up-front check (nothing changed): %w", err)
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
		if i < start {
			fmt.Fprintf(out, "%d. %s (already live; resuming)\n", i+1, s)
			continue
		}
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
				if err = r.Cluster.Execute(ctx, StopCompaction, store, c); err != nil {
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
				operation := StopSingle
				if store == "pyroscope" {
					operation = RetainMetastore
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
