package storemove

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Round 3: the routing manifests are an allow-list. Between the forward manifest and
// the original (both directions) the ONLY permitted differences are: -storageNode
// endpoints ADDED inside a container that already carries the flag (per container
// and per flag occurrence), and the exact -dedup.minScrapeInterval=1ms argument ADDED
// to a declared vmstorage workload. Everything else is refused before any apply.

func vmstorage(args string) string {
	return fmt.Sprintf("kind: StatefulSet\nmetadata: {name: vmstorage}\nspec:\n  replicas: 2\n  template:\n    spec:\n      containers:\n        - name: s\n          image: vm:1\n          args: [%s]\n", args)
}

func vmConfig() Config {
	cfg := config()
	cfg.Storage = []string{"statefulset/vmstorage"}
	return cfg
}

func TestCorrelation3DedupOnlyExactSettingOnVmstorage(t *testing.T) {
	const dedup = `"-dedup.minScrapeInterval=1ms"`
	routers := func(nodes string) string {
		return router("insert", "vm:1", nodes) + "---\n" + router("select", "vm:1", nodes)
	}
	cases := []struct {
		name, routing, original string
		ok                      bool
	}{
		{"1ms added on vmstorage with the new nodes", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", `+dedup), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), true},
		{"vmstorage unchanged", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), true},
		{"1ms already there on both sides", routers("a:8400,b:8400") + "---\n" + vmstorage(dedup), routers("a:8400") + "---\n" + vmstorage(dedup), true},
		{"1h on vmstorage", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", "-dedup.minScrapeInterval=1h"`), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), false},
		{"1ms duplicated on vmstorage (only the dedup count differs)", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", `+dedup+", "+dedup), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), false},
		{"1ms duplicated where one was (only the dedup count differs)", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", `+dedup+", "+dedup), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", `+dedup), false},
		{"changed from 1ms to 1h on vmstorage", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", "-dedup.minScrapeInterval=1h"`), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", `+dedup), false},
		{"removed from vmstorage (only the dedup argument differs)", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", `+dedup), false},
		{"exact 1ms on the insert router", strings.Replace(routers("a:8400,b:8400"), "\"-storageNode", dedup+", \"-storageNode", 1) + "---\n" + vmstorage(`"-retentionPeriod=1"`), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), false},
		{"1ms removed from the insert router", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), strings.Replace(routers("a:8400"), "\"-storageNode", dedup+", \"-storageNode", 1) + "---\n" + vmstorage(`"-retentionPeriod=1"`), false},
		{"another vmstorage flag changed", routers("a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=2", `+dedup), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), false},
		{"vmstorage image changed", strings.Replace(routers("a:8400,b:8400")+"---\n"+vmstorage(dedup), "image: vm:1\n          args: [\"-dedup", "image: vm:2\n          args: [\"-dedup", 1), routers("a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`), false},
		{"undeclared storage workload", routers("a:8400,b:8400") + "---\n" + strings.Replace(vmstorage(dedup), "vmstorage", "other", 1), routers("a:8400") + "---\n" + strings.Replace(vmstorage(`"-x=1"`), "vmstorage", "other", 1), false},
	}
	for _, tc := range cases {
		for _, step := range []Step{StorageRoute, RestoreRoute} {
			t.Run(tc.name+"/"+string(step), func(t *testing.T) {
				cfg := vmConfig()
				cfg.RoutingManifest = reviewManifest(t, tc.routing)
				cfg.OriginalRoutingManifest = reviewManifest(t, tc.original)
				setLive(t, true, cfg.OriginalRoutingManifest)
				var calls []string
				err := reviewKube(&calls).Execute(context.Background(), step, "victoriametrics", cfg)
				if tc.ok && (err != nil || len(calls) == 0) {
					t.Fatalf("allowed change refused: err=%v calls=%v", err, calls)
				}
				if !tc.ok && (err == nil || len(calls) != 0) {
					t.Fatalf("forbidden change accepted: err=%v calls=%v", err, calls)
				}
			})
		}
	}
}

// Each -storageNode flag occurrence of each container keeps its own endpoints.
func TestCorrelation3EndpointsPerContainerAndOccurrence(t *testing.T) {
	two := func(first, second string) string {
		return fmt.Sprintf("kind: Deployment\nmetadata: {name: insert}\nspec:\n  template:\n    spec:\n      containers:\n        - name: c\n          image: vm:1\n          args: [\"-storageNode=%s\", \"-x=1\", \"-storageNode=%s\"]\n", first, second)
	}
	initOnly := func(init, main string) string {
		return fmt.Sprintf("kind: Deployment\nmetadata: {name: insert}\nspec:\n  template:\n    spec:\n      initContainers:\n        - name: i\n          image: vm:1\n          command: [\"run\", \"-storageNode=%s\"]\n      containers:\n        - name: c\n          image: vm:1\n          args: [\"-storageNode=%s\"]\n", init, main)
	}
	cases := []struct {
		name, routing, original string
		ok                      bool
	}{
		{"endpoint added to the second occurrence", two("a:1", "b:1,c:1"), two("a:1", "b:1"), true},
		{"endpoint added to the first occurrence", two("a:1,c:1", "b:1"), two("a:1", "b:1"), true},
		{"endpoint moved between occurrences", two("b:1", "a:1,b:1"), two("a:1", "b:1"), false},
		{"occurrence removed", "kind: Deployment\nmetadata: {name: insert}\nspec:\n  template:\n    spec:\n      containers:\n        - name: c\n          image: vm:1\n          args: [\"-storageNode=a:1,b:1\", \"-x=1\"]\n", two("a:1", "b:1"), false},
		{"occurrence order swapped with other flag", strings.Replace(two("a:1", "b:1"), `"-x=1", "-storageNode=b:1"`, `"-storageNode=b:1", "-x=1"`, 1), two("a:1", "b:1"), false},
		{"init container keeps its endpoint", initOnly("a:1,z:1", "a:1"), initOnly("a:1", "a:1"), true},
		{"init container endpoint moved to main", initOnly("z:1", "a:1,a2:1"), initOnly("a:1", "a:1"), false},
		{"flag added to a container that had none", strings.Replace(two("a:1", "b:1"), "args: [", "args: [\"-storageNode=new:1\", ", 1), two("a:1", "b:1"), false},
		{"container renamed", strings.Replace(two("a:1", "b:1,c:1"), "name: c", "name: d", 1), two("a:1", "b:1"), false},
	}
	for _, tc := range cases {
		for _, step := range []Step{StorageRoute, RestoreRoute} {
			t.Run(tc.name+"/"+string(step), func(t *testing.T) {
				cfg := config()
				cfg.Routers = []string{"deployment/insert"}
				cfg.RoutingManifest = reviewManifest(t, tc.routing)
				cfg.OriginalRoutingManifest = reviewManifest(t, tc.original)
				setLive(t, true, cfg.OriginalRoutingManifest)
				var calls []string
				err := reviewKube(&calls).Execute(context.Background(), step, "victoriametrics", cfg)
				if tc.ok && (err != nil || len(calls) == 0) {
					t.Fatalf("allowed change refused: err=%v calls=%v", err, calls)
				}
				if !tc.ok && (err == nil || len(calls) != 0) {
					t.Fatalf("forbidden change accepted: err=%v calls=%v", err, calls)
				}
			})
		}
	}
}

// Router object sets must match in both directions, whichever side has the extra one.
func TestCorrelation3ObjectSetsMatchBothDirections(t *testing.T) {
	both := router("insert", "vm:1", "a:8400") + "---\n" + router("select", "vm:1", "a:8400")
	bothNew := router("insert", "vm:1", "a:8400,b:8400") + "---\n" + router("select", "vm:1", "a:8400,b:8400")
	only := router("insert", "vm:1", "a:8400,b:8400")
	for _, tc := range []struct{ name, routing, original string }{
		{"original-only select", only, both},
		{"routing-only select", bothNew, router("insert", "vm:1", "a:8400")},
		{"original-only storage workload", bothNew, both + "---\n" + vmstorage(`"-x=1"`)},
		{"routing-only storage workload", bothNew + "---\n" + vmstorage(`"-x=1"`), both},
	} {
		for _, step := range []Step{StorageRoute, RestoreRoute} {
			t.Run(tc.name+"/"+string(step), func(t *testing.T) {
				cfg := vmConfig()
				cfg.RoutingManifest = reviewManifest(t, tc.routing)
				cfg.OriginalRoutingManifest = reviewManifest(t, tc.original)
				setLive(t, true, cfg.OriginalRoutingManifest)
				var calls []string
				if err := reviewKube(&calls).Execute(context.Background(), step, "victoriametrics", cfg); err == nil || len(calls) != 0 {
					t.Fatalf("mismatched object sets applied: err=%v calls=%v", err, calls)
				}
			})
		}
	}
}

func decodeDocs(t *testing.T, text string) []map[string]any {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(text))
	var docs []map[string]any
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		docs = append(docs, m)
	}
	return docs
}
func encodeDocs(t *testing.T, docs []map[string]any) string {
	t.Helper()
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			t.Fatal(err)
		}
	}
	enc.Close()
	return sb.String()
}

// walk collects every map in the tree so a random field can be planted anywhere.
func walkMaps(v any, out *[]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		*out = append(*out, x)
		for _, c := range x {
			walkMaps(c, out)
		}
	case []any:
		for _, c := range x {
			walkMaps(c, out)
		}
	}
}

// A random extra field changed in ANY object of EITHER routing manifest (forward or
// original) is refused: the allow-list is the only way through.
func TestCorrelation3RandomExtraFieldInAnyRoutingManifestRefused(t *testing.T) {
	forward := router("insert", "vm:1", "a:8400,b:8400") + "---\n" + router("select", "vm:1", "a:8400,b:8400") + "---\n" + vmstorage(`"-retentionPeriod=1", "-dedup.minScrapeInterval=1ms"`)
	original := router("insert", "vm:1", "a:8400") + "---\n" + router("select", "vm:1", "a:8400") + "---\n" + vmstorage(`"-retentionPeriod=1"`)
	run := func(t *testing.T, fwd, orig string, step Step) error {
		cfg := vmConfig()
		cfg.RoutingManifest = reviewManifest(t, fwd)
		cfg.OriginalRoutingManifest = reviewManifest(t, orig)
		setLive(t, true, cfg.OriginalRoutingManifest)
		var calls []string
		err := reviewKube(&calls).Execute(context.Background(), step, "victoriametrics", cfg)
		if err != nil && len(calls) != 0 {
			t.Fatalf("refused only after kubectl ran: %v", calls)
		}
		if err == nil && len(calls) == 0 {
			t.Fatal("accepted without applying anything")
		}
		return err
	}
	if err := run(t, forward, original, StorageRoute); err != nil {
		t.Fatalf("control (declared changes only) refused: %v", err)
	}
	rng := rand.New(rand.NewSource(110))
	fixed := []func(m map[string]any){
		func(m map[string]any) { m["extra"] = 1 },
		func(m map[string]any) { m["metadata"].(map[string]any)["annotations"] = map[string]any{"x": "y"} },
		func(m map[string]any) { m["spec"].(map[string]any)["replicas"] = 9 },
		func(m map[string]any) { m["spec"].(map[string]any)["strategy"] = map[string]any{"type": "Recreate"} },
		func(m map[string]any) {
			m["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["nodeSelector"] = map[string]any{"a": "b"}
		},
		func(m map[string]any) {
			c := m["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
			c["env"] = []any{map[string]any{"name": "A", "value": "b"}}
		},
		func(m map[string]any) {
			c := m["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
			c["image"] = "vm:evil"
		},
		func(m map[string]any) {
			p := m["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			p["containers"] = append(p["containers"].([]any), map[string]any{"name": "extra", "image": "x"})
		},
	}
	for objIndex, name := range []string{"insert", "select", "vmstorage"} {
		for side, text := range map[string]string{"forward": forward, "original": original} {
			for i := 0; i < 200; i++ {
				docs := decodeDocs(t, text)
				if len(docs) != 3 {
					t.Fatalf("fixture has %d docs", len(docs))
				}
				o := docs[objIndex]
				if i < len(fixed) {
					fixed[i](o)
				} else {
					var maps []map[string]any
					walkMaps(o, &maps)
					maps[rng.Intn(len(maps))][fmt.Sprintf("zz-random-%d", rng.Intn(1000))] = rng.Intn(10)
				}
				mutated := encodeDocs(t, docs)
				fwd, orig := forward, original
				if side == "forward" {
					fwd = mutated
				} else {
					orig = mutated
				}
				for _, step := range []Step{StorageRoute, RestoreRoute} {
					if err := run(t, fwd, orig, step); err == nil {
						t.Fatalf("%s %s mutation %d accepted at %s:\n%s", side, name, i, step, mutated)
					}
				}
			}
		}
	}
}

// The other three manifests: scaled_manifest may carry only the scaled workloads and the
// store's own supporting objects (headless Service, ConfigMap); single_manifest and
// metastore_manifest only the single. Never a stable Service or another kind.
func TestCorrelation3OtherManifestsAllowListObjects(t *testing.T) {
	scaledOK := "kind: StatefulSet\nmetadata: {name: new, labels: {obs.diene/store-move: tempo}}\nspec: {replicas: 1}\n"
	singleOK := "kind: StatefulSet\nmetadata: {name: single}\nspec: {replicas: 1}\n"
	extras := map[string]string{
		"ClusterRole":      "kind: ClusterRole\nmetadata: {name: new-x}\n",
		"Namespace":        "kind: Namespace\nmetadata: {name: new-x}\n",
		"CronJob":          "kind: CronJob\nmetadata: {name: new-x}\n",
		"Ingress":          "kind: Ingress\nmetadata: {name: new-x}\n",
		"NetworkPolicy":    "kind: NetworkPolicy\nmetadata: {name: new-x}\n",
		"PVC":              "kind: PersistentVolumeClaim\nmetadata: {name: new-data, labels: {obs.diene/store-move: tempo}}\n",
		"Secret":           "kind: Secret\nmetadata: {name: new-secret, labels: {obs.diene/store-move: tempo}}\n",
		"stable Service":   "kind: Service\nmetadata: {name: tempo-query}\n",
		"alloy workload":   "kind: Deployment\nmetadata: {name: alloy}\nspec: {replicas: 1}\n",
		"unlisted sts":     "kind: StatefulSet\nmetadata: {name: other}\nspec: {replicas: 1}\n",
		"stable endpoints": "kind: Endpoints\nmetadata: {name: tempo-distributor}\n",
	}
	supporting := map[string]string{
		"headless Service": "kind: Service\nmetadata: {name: new-headless, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true}\n",
		"ConfigMap":        "kind: ConfigMap\nmetadata: {name: new-config, labels: {obs.diene/store-move: tempo}}\n",
	}
	type target struct {
		name string
		ok   string
		set  func(c *Config, f string)
		step Step
	}
	targets := []target{
		{"scaled", scaledOK, func(c *Config, f string) { c.ScaledManifest = f }, Prepare},
		{"metastore", singleOK, func(c *Config, f string) { c.MetastoreManifest = f }, Step("retain single as metastore")},
		{"single", singleOK, func(c *Config, f string) { c.SingleManifest = f }, Restore},
	}
	for _, tg := range targets {
		run := func(t *testing.T, body string) ([]string, error) {
			cfg := config()
			tg.set(&cfg, reviewManifest(t, body))
			if tg.name != "scaled" {
				setLive(t, true, reviewManifest(t, singleOK))
			}
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), tg.step, "tempo", cfg)
			return calls, err
		}
		t.Run(tg.name+" baseline", func(t *testing.T) {
			if _, err := run(t, tg.ok); err != nil {
				t.Fatalf("unchanged baseline refused: %v", err)
			}
		})
		for name, extra := range extras {
			t.Run(tg.name+" refuses "+name, func(t *testing.T) {
				if calls, err := run(t, tg.ok+"---\n"+extra); err == nil || len(calls) != 0 {
					t.Fatalf("extra object applied: err=%v calls=%v", err, calls)
				}
			})
		}
		for name, extra := range supporting {
			t.Run(tg.name+" supporting "+name, func(t *testing.T) {
				calls, err := run(t, tg.ok+"---\n"+extra)
				if tg.name == "scaled" && err != nil {
					t.Fatalf("store's own new object refused: %v", err)
				}
				if tg.name != "scaled" && (err == nil || len(calls) != 0) {
					t.Fatalf("%s manifest may carry only the single; applied: err=%v calls=%v", tg.name, err, calls)
				}
			})
		}
	}
	for _, name := range []string{"metastore", "single"} {
		t.Run(name+" without the single", func(t *testing.T) {
			cfg := config()
			f := reviewManifest(t, "kind: ConfigMap\nmetadata: {name: only-config}\n")
			step := Restore
			cfg.SingleManifest = f
			if name == "metastore" {
				cfg.MetastoreManifest = f
				step = Step("retain single as metastore")
			}
			var calls []string
			if err := reviewKube(&calls).Execute(context.Background(), step, "tempo", cfg); err == nil || len(calls) != 0 {
				t.Fatalf("manifest without the single applied: err=%v calls=%v", err, calls)
			}
		})
	}
}

// Apply semantics: whatever file reaches `kubectl apply` must carry the single at
// replicas 0 (an absent replicas field defaults to 1), so no pod starts before the
// template roles are patched and the explicit scale-up.
func TestCorrelation3RestoreAppliesSingleAtZeroReplicas(t *testing.T) {
	for name, body := range map[string]string{
		"replicas 1":      "kind: StatefulSet\nmetadata: {name: single, labels: {keep: me}}\nspec:\n  replicas: 1\n  serviceName: s\n  template:\n    metadata:\n      labels: {app: x}\n",
		"replicas absent": "kind: StatefulSet\nmetadata: {name: single}\nspec:\n  serviceName: s\n",
		"inside a List":   "kind: List\nitems:\n  - kind: StatefulSet\n    metadata: {name: single}\n    spec: {replicas: 3, serviceName: s}\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config()
			cfg.SingleManifest = reviewManifest(t, body)
			setLive(t, true, cfg.SingleManifest)
			var calls []string
			base := reviewKube(&calls)
			var applied string
			k := Kubernetes{Command: func(ctx context.Context, args ...string) ([]byte, error) {
				line := strings.Join(args, " ")
				if i := strings.Index(line, "apply -f "); i >= 0 {
					b, err := os.ReadFile(strings.TrimSpace(line[i+len("apply -f "):]))
					if err != nil {
						t.Fatalf("applied file unreadable at apply time: %v", err)
					}
					applied = string(b)
				}
				return base.Command(ctx, args...)
			}}
			if err := k.Execute(context.Background(), Restore, "tempo", cfg); err != nil {
				t.Fatal(err)
			}
			var single map[string]any
			var others int
			for _, d := range decodeDocs(t, applied) {
				var objs []map[string]any
				if d["kind"] == "List" {
					for _, it := range d["items"].([]any) {
						objs = append(objs, it.(map[string]any))
					}
				} else {
					objs = append(objs, d)
				}
				for _, o := range objs {
					if o["kind"] == "StatefulSet" {
						single = o
					} else {
						others++
					}
				}
			}
			if single == nil {
				t.Fatalf("single missing from applied file:\n%s", applied)
			}
			if r, ok := single["spec"].(map[string]any)["replicas"]; !ok || r != 0 {
				t.Fatalf("applied single replicas = %v, want explicit 0:\n%s", r, applied)
			}
			if name == "replicas 1" {
				if others != 0 || !strings.Contains(applied, "keep: me") || !strings.Contains(applied, "serviceName: s") {
					t.Fatalf("rewrite dropped other fields/objects:\n%s", applied)
				}
			}
			scale, patch := -1, -1
			for i, c := range calls {
				if strings.Contains(c, "scale statefulset/single --replicas=1") {
					scale = i
				}
				if strings.Contains(c, "patch statefulset/single") && strings.Contains(c, `"obs.diene/tempo-read":"true"`) && patch < 0 {
					patch = i
				}
			}
			if patch < 0 || scale < 0 || patch > scale {
				t.Fatalf("want roles patch before scale-up, got patch %d scale %d: %v", patch, scale, calls)
			}
			// The user's file is never rewritten.
			if b, _ := os.ReadFile(cfg.SingleManifest); string(b) != body {
				t.Fatalf("single manifest on disk was modified")
			}
		})
	}
}

func TestCorrelation3StorageOnlyForVictoriaMetrics(t *testing.T) {
	cfg := vmConfig()
	cfg.Routers = []string{"deployment/insert", "deployment/select"}
	cfg.RoutingManifest, cfg.OriginalRoutingManifest, cfg.MetricsURL = "r.yaml", "o.yaml", "http://x"
	if err := cfg.Validate("victoriametrics", false); err != nil {
		t.Fatalf("victoriametrics storage list refused: %v", err)
	}
	if err := cfg.Validate("victorialogs", false); err == nil {
		t.Fatal("storage list accepted for victorialogs, which has no dedup step")
	}
}
