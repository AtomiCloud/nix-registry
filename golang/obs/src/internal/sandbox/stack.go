package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

// LabelKey is the label every container, volume and network of a sandbox carries.
const LabelKey = "obs.sandbox"

// Config is one sandbox run.
type Config struct {
	Project  string // the label value; names derive from it
	Dir      string // the observability folder (dashboards, seed)
	SeedPath string // default <Dir>/seed.yaml
	PortBase int    // 0: the components' own ports (3000, 8428, ...); else base+offset
	Reseed   bool   // load the seed again even when a sandbox with a seed is already up
	Timeout  time.Duration

	Runner Runner
	HTTP   *http.Client
	Out    io.Writer
	Now    func() time.Time
	// StateDir holds the state file; default <user cache dir>/obs.
	StateDir string
	// HostURL overrides where obs reaches a component from the host (tests).
	HostURL func(Component) string
}

// Info is what a running sandbox tells its users.
type Info struct {
	Project string            `json:"project"`
	Grafana string            `json:"grafana"`
	URLs    map[string]string `json:"urls"` // component name -> base URL
	// From and To are the seed's time line in unix ms (0 when no seed was loaded).
	From int64 `json:"from_ms"`
	To   int64 `json:"to_ms"`
	// Datasources maps a kind (metrics, logs, traces, profiles) to the Grafana datasource uids.
	Datasources map[string][]string `json:"datasources"`
	Reused      bool                `json:"reused"`
}

var nameRe = regexp.MustCompile(`[^a-z0-9]+`)

// ProjectName makes a label-safe project name from s ("My Repo" -> "my-repo").
func ProjectName(s string) string {
	s = strings.Trim(nameRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 30 {
		s = strings.Trim(s[:30], "-")
	}
	if s == "" {
		s = "default"
	}
	return s
}

// Prefix is the start of every name this sandbox makes.
func Prefix(project string) string { return "obs-sandbox-" + project }

func containerName(project string, c Component) string { return Prefix(project) + "-" + c.Name }

func (c *Config) defaults() {
	if c.Timeout == 0 {
		c.Timeout = 240 * time.Second
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if c.Out == nil {
		c.Out = io.Discard
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Project == "" {
		c.Project = "default"
	}
	if c.HostURL == nil {
		base := c.PortBase
		c.HostURL = func(k Component) string { return fmt.Sprintf("http://127.0.0.1:%d", hostPort(base, k)) }
	}
}

func hostPort(base int, c Component) int {
	if base == 0 {
		return c.Port
	}
	return base + c.Offset
}

func (c *Config) labelArgs(comp string) []string {
	a := []string{"--label", LabelKey + "=" + c.Project}
	if comp != "" {
		a = append(a, "--label", LabelKey+".component="+comp)
	}
	return a
}

// existing is a container this sandbox already has.
type existing struct{ Image, State string }

func (c *Config) list(ctx context.Context) (map[string]existing, error) {
	out, err := c.Runner.Run(ctx, "ps", "-a", "--filter", "label="+LabelKey+"="+c.Project, "--format", "{{.Names}}|{{.Image}}|{{.State}}")
	if err != nil {
		return nil, err
	}
	m := map[string]existing{}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		p := strings.Split(strings.TrimSpace(ln), "|")
		if len(p) != 3 {
			continue
		}
		m[p[0]] = existing{Image: p[1], State: p[2]}
	}
	return m, nil
}

// sameImage compares an image as the runtime reports it with the pin (docker may add
// docker.io/ in front, podman does).
func sameImage(have, want string) bool {
	have = strings.TrimPrefix(strings.TrimPrefix(have, "docker.io/"), "library/")
	return have == want
}

// checkExisting refuses a sandbox whose containers run other images than the pins.
func checkExisting(c *Config, have map[string]existing) error {
	var bad []string
	for _, k := range Pins {
		e, ok := have[containerName(c.Project, k)]
		if ok && !sameImage(e.Image, k.Ref()) {
			bad = append(bad, fmt.Sprintf("%s runs %s, the pin is %s", containerName(c.Project, k), e.Image, k.Ref()))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("refusing: image tags differ from the pins: %s; run `obs sandbox down` first", strings.Join(bad, "; "))
	}
	return nil
}

// VMRelabelConfig is the sandbox VictoriaMetrics' -relabelConfig, the same labelmap as the real
// vminsert (concept 2): a scraped sample reaches the store with durian_cloud_* labels (Alloy drops a
// relabel rule whose target_label has dots) and the labelmap turns them into the stored durian.cloud.*
// labels. The labeldrop removes the underscore originals, which labelmap keeps, so only the dotted
// spelling is stored and a query written with the wrong spelling finds nothing, as in production.
const VMRelabelConfig = `- action: labelmap
  regex: "durian_cloud_(.+)"
  replacement: "durian.cloud.$1"
- action: labeldrop
  regex: "durian_cloud_.+"
`

// vmRelabelPath is where VMRelabelConfig is copied into the VictoriaMetrics container.
const vmRelabelPath = "/etc/vm-relabel.yml"

const tempoConfig = `server:
  http_listen_port: 3200
distributor:
  max_attribute_bytes: 16384
  receivers:
    otlp:
      protocols:
        grpc:
          endpoint: 0.0.0.0:4317
        http:
          endpoint: 0.0.0.0:4318
storage:
  trace:
    backend: local
    wal:
      path: /var/tempo/wal
    local:
      path: /var/tempo/blocks
live_store:
  max_block_duration: 5m
`

// mount is one named volume of a component.
type mount struct{ Name, Path string }

// volumes lists every labelled named volume a component gets: the data directory (named
// obs-sandbox-<project>-<component>) and every other VOLUME the image declares
// (<that name><path with / as ->). Nothing is left to become an anonymous volume.
func (c *Config) volumes(k Component) []mount {
	name := containerName(c.Project, k)
	out := []mount{{name, k.Data}}
	for _, p := range k.Declared {
		if p != k.Data {
			out = append(out, mount{name + strings.ReplaceAll(p, "/", "-"), p})
		}
	}
	return out
}

// createArgs is the `create` command line of one component.
func (c *Config) createArgs(k Component) []string {
	name := containerName(c.Project, k)
	a := []string{"create", "--name", name}
	a = append(a, c.labelArgs(k.Name)...)
	a = append(a, "--network", Prefix(c.Project),
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort(c.PortBase, k), k.Port))
	for _, v := range c.volumes(k) {
		a = append(a, "-v", v.Name+":"+v.Path)
	}
	switch k.Name {
	case "grafana":
		for _, e := range []string{
			"GF_SECURITY_ADMIN_USER=admin", "GF_SECURITY_ADMIN_PASSWORD=admin",
			"GF_AUTH_ANONYMOUS_ENABLED=true", "GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer",
			"GF_ANALYTICS_REPORTING_ENABLED=false", "GF_ANALYTICS_CHECK_FOR_UPDATES=false",
			"GF_INSTALL_PLUGINS=" + PluginID + " " + PluginVersion,
		} {
			a = append(a, "-e", e)
		}
	}
	a = append(a, k.Ref())
	switch k.Name {
	case "vm":
		// Concept section 10.4 single-instance settings: 30 d retention, 30 labels per series,
		// 4 concurrent queries, 60 % of memory for caches.
		a = append(a, "-retentionPeriod=30d", "-storageDataPath="+k.Data,
			"-maxLabelsPerTimeseries=30", "-search.maxConcurrentRequests=4", "-memory.allowedPercent=60",
			"-relabelConfig="+vmRelabelPath)
	case "vl":
		// Concept section 10.4: 30 d retention and the 16 GiB laptop disk cap (oldest days go first),
		// 4 concurrent queries, queue 10 s and query 30 s (the query bounds), 60 % of memory.
		a = append(a, "-retentionPeriod=30d", "-storageDataPath="+k.Data,
			fmt.Sprintf("-retention.maxDiskSpaceUsageBytes=%d", int64(LaptopVLDiskCap)),
			"-search.maxConcurrentRequests=4", "-search.maxQueueDuration=10s", "-search.maxQueryDuration=30s",
			"-memory.allowedPercent=60")
	case "tempo":
		a = append(a, "-config.file=/etc/tempo.yaml", "-target=all")
	case "pyroscope":
		// One process, the v2 storage, and no start-up wait (a single instance has no cluster to wait for).
		a = append(a, "-target=all", "-architecture.storage=v2", "-metastore.min-ready-duration=0", "-segment-writer.min-ready-duration=0")
	}
	return a
}

// Up starts (or reuses) the sandbox, loads the dashboards and the seed, and returns
// where everything is. A pin mismatch is an error and starts nothing.
func Up(ctx context.Context, cfg Config) (*Info, error) {
	cfg.defaults()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	set, err := files.Load(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("observability folder: %w", err)
	}
	have, err := cfg.list(ctx)
	if err != nil {
		return nil, fmt.Errorf("container runtime: %w", err)
	}
	if err := checkExisting(&cfg, have); err != nil {
		return nil, err
	}
	created := false
	if _, err := cfg.Runner.Run(ctx, "network", "inspect", Prefix(cfg.Project)); err != nil {
		if _, err := cfg.Runner.Run(ctx, "network", "create", "--label", LabelKey+"="+cfg.Project, Prefix(cfg.Project)); err != nil {
			return nil, err
		}
	}
	for _, k := range Pins {
		name := containerName(cfg.Project, k)
		e, ok := have[name]
		switch {
		case ok && e.State == "running":
			continue
		case ok:
			fmt.Fprintf(cfg.Out, "starting %s\n", name)
			if _, err := cfg.Runner.Run(ctx, "start", name); err != nil {
				return nil, err
			}
			continue
		}
		created = true
		if _, err := cfg.Runner.Run(ctx, "image", "inspect", k.Ref()); err != nil {
			fmt.Fprintf(cfg.Out, "pulling %s\n", k.Ref())
			if _, err := cfg.Runner.Run(ctx, "pull", "-q", k.Ref()); err != nil {
				return nil, err
			}
		}
		if err := cfg.checkDeclaredVolumes(ctx, k); err != nil {
			return nil, err
		}
		for _, v := range cfg.volumes(k) {
			if _, err := cfg.Runner.Run(ctx, append([]string{"volume", "create"}, append(cfg.labelArgs(""), v.Name)...)...); err != nil {
				return nil, err
			}
		}
		if _, err := cfg.Runner.Run(ctx, cfg.createArgs(k)...); err != nil {
			return nil, err
		}
		if k.Name == "tempo" {
			if err := copyIn(ctx, cfg.Runner, name, "/etc/tempo.yaml", tempoConfig); err != nil {
				return nil, err
			}
		}
		if k.Name == "vm" {
			if err := copyIn(ctx, cfg.Runner, name, vmRelabelPath, VMRelabelConfig); err != nil {
				return nil, err
			}
		}
		if _, err := cfg.Runner.Run(ctx, "start", name); err != nil {
			return nil, err
		}
	}

	info := &Info{Project: cfg.Project, URLs: map[string]string{}, Reused: !created}
	for _, k := range Pins {
		info.URLs[k.Name] = cfg.HostURL(k)
	}
	info.Grafana = info.URLs["grafana"]
	fmt.Fprintf(cfg.Out, "waiting for the stack to answer\n")
	if err := waitReady(ctx, cfg.HTTP, info.URLs); err != nil {
		return nil, err
	}
	if err := VerifyVersions(ctx, cfg.HTTP, info.URLs); err != nil {
		return nil, err
	}

	st := readState(cfg)
	dss := Datasources(set, cfg.Project)
	info.Datasources = map[string][]string{}
	for _, d := range dss {
		info.Datasources[d.Kind] = append(info.Datasources[d.Kind], d.UID)
	}
	if err := Provision(ctx, cfg.HTTP, info.Grafana, dss); err != nil {
		return nil, err
	}
	n, err := ImportDashboards(ctx, cfg.HTTP, info.Grafana, set)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(cfg.Out, "loaded %d dashboard(s) into Grafana\n", n)

	if st != nil && !created && !cfg.Reseed && st.From != 0 {
		info.From, info.To = st.From, st.To
		fmt.Fprintf(cfg.Out, "seed already loaded (kept; --reseed loads it again)\n")
	} else if err := loadSeed(ctx, &cfg, info); err != nil {
		return nil, err
	}
	writeState(cfg, info)
	return info, nil
}

func loadSeed(ctx context.Context, cfg *Config, info *Info) error {
	p := cfg.SeedPath
	if p == "" {
		p = filepath.Join(cfg.Dir, "seed.yaml")
	}
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(cfg.Out, "no seed file at %s: the stores start empty\n", p)
		return nil
	}
	sd, err := seed.Load(p)
	if err != nil {
		return err
	}
	// The time line ends now (rounded down to the minute), so a dashboard's last-hours view has data.
	to := cfg.Now().UTC().Truncate(time.Minute)
	from := to.Add(-sd.Duration())
	ds, err := seed.Build(sd, from)
	if err != nil {
		return err
	}
	fmt.Fprintf(cfg.Out, "loading the seed (%s of data)\n", sd.Length)
	if err := LoadSeed(ctx, cfg.HTTP, info.URLs["vm"], info.URLs["vl"], ds); err != nil {
		return err
	}
	info.From, info.To = from.UnixMilli(), to.UnixMilli()
	return nil
}

// checkDeclaredVolumes refuses an image that declares a VOLUME the pin list does not know:
// that path would become an anonymous volume no label finds. It guards a pin bump.
func (c *Config) checkDeclaredVolumes(ctx context.Context, k Component) error {
	out, err := c.Runner.Run(ctx, "image", "inspect", "--format", "{{json .Config.Volumes}}", k.Ref())
	if err != nil {
		return nil // the earlier image inspect already proved the image exists; a runtime that cannot format is not a reason to stop
	}
	var have map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &have) != nil {
		return nil // "null" or empty: no VOLUME
	}
	known := map[string]bool{k.Data: true}
	for _, p := range k.Declared {
		known[p] = true
	}
	var extra []string
	for p := range have {
		if !known[p] {
			extra = append(extra, p)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		return fmt.Errorf("refusing: image %s declares VOLUME %s that obs does not mount from a named volume (add it to Declared in pins.go)", k.Ref(), strings.Join(extra, ", "))
	}
	return nil
}

func copyIn(ctx context.Context, r Runner, container, dest, content string) error {
	dir, err := os.MkdirTemp("", "obs-sandbox-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	f := filepath.Join(dir, filepath.Base(dest))
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		return err
	}
	_, err = r.Run(ctx, "cp", f, container+":"+dest)
	return err
}

// Down removes this project's containers, volumes and network, found by label and
// double-checked by name. It never prunes and touches nothing else.
func Down(ctx context.Context, cfg Config) error {
	cfg.defaults()
	prefix := Prefix(cfg.Project)
	var firstErr error
	rm := func(kind string, listArgs []string, rmArgs ...string) {
		out, err := cfg.Runner.Run(ctx, listArgs...)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		var names []string
		for _, n := range strings.Fields(out) {
			// The label found it; the name check is the second guard: only our own names go.
			if n == prefix || strings.HasPrefix(n, prefix+"-") {
				names = append(names, n)
			} else {
				fmt.Fprintf(cfg.Out, "skipping %s %s: labelled for this sandbox but not named %s*\n", kind, n, prefix)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			if _, err := cfg.Runner.Run(ctx, append(rmArgs, n)...); err != nil && firstErr == nil {
				firstErr = err
			}
			fmt.Fprintf(cfg.Out, "removed %s %s\n", kind, n)
		}
	}
	label := "label=" + LabelKey + "=" + cfg.Project
	rm("container", []string{"ps", "-a", "--filter", label, "--format", "{{.Names}}"}, "rm", "-f", "-v")
	rm("network", []string{"network", "ls", "--filter", label, "--format", "{{.Name}}"}, "network", "rm")
	rm("volume", []string{"volume", "ls", "--filter", label, "--format", "{{.Name}}"}, "volume", "rm")
	removeState(cfg)
	return firstErr
}

// Status reports whether the sandbox is up, from the state file and one Grafana probe.
func Status(ctx context.Context, cfg Config) (*Info, error) {
	cfg.defaults()
	st := readState(cfg)
	if st == nil {
		return nil, errors.New("no sandbox state for project " + cfg.Project + ": run `obs sandbox up`")
	}
	if err := probe(ctx, cfg.HTTP, st.Grafana+"/api/health"); err != nil {
		return st, fmt.Errorf("sandbox %s is not answering at %s: %w", cfg.Project, st.Grafana, err)
	}
	return st, nil
}
