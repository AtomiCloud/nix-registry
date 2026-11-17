package storemove

import (
	"fmt"
	"strings"
)

// The builders below are every change obs store move makes to an existing object. Each
// reads the live object, refuses it unless it is in the state the change starts from,
// and returns `test` operations for every old value the change relies on, followed by
// the mutations. The inverse is generated (invert) and stored with the patch (withInverse).

const (
	dedupName  = "dedup.minScrapeInterval"
	dedupExact = "-dedup.minScrapeInterval=1ms"
)

func rolePath(role string) string {
	return pointer("spec", "template", "metadata", "labels", role)
}

// singleReplicas refuses a single that does not run exactly one replica: the original
// replica count obs restores is then fixed independently of anything stored.
func singleReplicas(n int, what string) error {
	if n != 1 {
		return fmt.Errorf("%s: live replicas %d; a single-instance store runs exactly 1 (obs restores it to 1)", what, n)
	}
	return nil
}

// pinReplicas pins the live replica count and returns it.
func pinReplicas(live map[string]any, what string) (int, []op, error) {
	v, ok := mapAt(live, "spec")["replicas"]
	n, isInt := intField(v)
	if !ok || !isInt {
		return 0, nil, fmt.Errorf("%s: live spec.replicas missing or not an integer", what)
	}
	return n, []op{{"test", "/spec/replicas", n}}, nil
}

// pinRetain refuses a live PersistentVolumeClaim retention policy of Delete (whenScaled
// deletes the claims of scaled-down pods, whenDeleted those of a deleted StatefulSet) and
// pins every policy key the object has to Retain.
func pinRetain(live map[string]any, what string) ([]op, error) {
	p := mapAt(live, "spec", "persistentVolumeClaimRetentionPolicy")
	var ops []op
	for _, k := range sortedKeys(p) {
		if s := scalar(p[k]); s != "Retain" {
			return nil, fmt.Errorf("%s: live persistentVolumeClaimRetentionPolicy.%s=%s; obs never stops or converts a store whose claims could be deleted (set Retain first)", what, k, s)
		}
		ops = append(ops, op{"test", pointer("spec", "persistentVolumeClaimRetentionPolicy", k), "Retain"})
	}
	return ops, nil
}

// rolesTo pins every role label of the pod template at from and sets it to to.
func rolesTo(live map[string]any, roles []string, from, to, what string) ([]op, error) {
	tpl := stringMap(mapAt(live, "spec", "template", "metadata")["labels"])
	var ops []op
	for _, role := range roles {
		v, ok := tpl[role]
		if !ok || v != from {
			return nil, fmt.Errorf("%s: pod template label %s=%q, the change starts from %q", what, role, v, from)
		}
		ops = append(ops, op{"test", rolePath(role), from}, op{"replace", rolePath(role), to})
	}
	return ops, nil
}

// stopSingle (Tempo, after the drain): replicas N -> 0 and every template role
// true -> false in one patch, so a restart can never rejoin the Services.
func stopSingle(c Config) builder {
	return func(live map[string]any) ([]op, error) {
		what := "single " + c.Single
		n, ops, err := pinReplicas(live, what)
		if err != nil {
			return nil, err
		}
		if err := singleReplicas(n, what); err != nil {
			return nil, err
		}
		retain, err := pinRetain(live, what)
		if err != nil {
			return nil, err
		}
		roles, err := rolesTo(live, c.RoleLabels, "true", "false", what)
		if err != nil {
			return nil, err
		}
		ops = append(append(ops, retain...), op{"replace", "/spec/replicas", 0})
		return append(ops, roles...), nil
	}
}

// retainMetastore (Pyroscope): -target=all -> -target=metastore (same spelling) and every
// template role true -> false; replicas (at least 1), claims, raft identity and every
// other field stay as they are, because the patch names nothing else.
func retainMetastore(c Config) builder {
	return func(live map[string]any) ([]op, error) {
		what := "single " + c.Single
		n, ops, err := pinReplicas(live, what)
		if err != nil {
			return nil, err
		}
		if err := singleReplicas(n, what); err != nil {
			return nil, err
		}
		retain, err := pinRetain(live, what)
		if err != nil {
			return nil, err
		}
		hits, err := findFlags(live, "target")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		if len(hits) != 1 || hits[0].tok.value != "all" {
			return nil, fmt.Errorf("%s: want exactly one -target=all flag in its containers, found %d (%v)", what, len(hits), targetValues(hits))
		}
		roles, err := rolesTo(live, c.RoleLabels, "true", "false", what)
		if err != nil {
			return nil, err
		}
		ops = append(append(ops, retain...), setFlagValue(hits[0], "metastore")...)
		return append(ops, roles...), nil
	}
}
func targetValues(hits []flagHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.tok.value)
	}
	return out
}

// scaleToZero (a separate Tempo compactor): replicas N -> 0; the rollback restores N.
func scaleToZero(what string) builder {
	return func(live map[string]any) ([]op, error) {
		n, ops, err := pinReplicas(live, what)
		if err != nil {
			return nil, err
		}
		if n < 1 {
			return nil, fmt.Errorf("%s: live replicas %d, want a running compactor", what, n)
		}
		return append(ops, op{"replace", "/spec/replicas", 0}), nil
	}
}

// addStorageNodes (VictoriaLogs/VictoriaMetrics insert/select): the one -storageNode flag
// gains the derived endpoints of the new storage nodes (same spelling, every existing
// endpoint kept in order). For VictoriaMetrics with two copies, vminsert also gains
// -replicationFactor=2 (concept 10.5), unless the live router already has exactly 2, and
// vmselect gains -dedup.minScrapeInterval=1ms: the pinned v1.156.0 docs require it on
// vmselect whenever replication is enabled (docs/victoriametrics/Cluster-VictoriaMetrics.md
// "Replication and data safety": "-dedup.minScrapeInterval=1ms command-line flag must be
// passed to vmselect nodes when the replication is enabled"; vminsert -replicationFactor
// help: "vmselect must run with -dedup.minScrapeInterval=1ms").
func addStorageNodes(c Config, r Router, derived []string) builder {
	return func(live map[string]any) ([]op, error) {
		what := "router " + r.Workload
		hits, err := findFlags(live, "storageNode", "replicationFactor")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		if len(hits) != 1 {
			return nil, fmt.Errorf("%s: want exactly one -storageNode flag in its containers, found %d", what, len(hits))
		}
		h := hits[0]
		old := strings.Split(h.tok.value, ",")
		have := map[string]bool{}
		for _, e := range old {
			if strings.TrimSpace(e) == "" || strings.Contains(e, "%{") {
				return nil, fmt.Errorf("%s: -storageNode %q: empty or %%{ENV} endpoint; obs only extends a literal endpoint list", what, h.tok.value)
			}
			have[e] = true
		}
		for _, e := range derived {
			if have[e] {
				return nil, fmt.Errorf("%s: -storageNode already lists %s, but not as a change obs made (no %s annotation)", what, e, inverseAnnotation)
			}
		}
		ops := setFlagValue(h, h.tok.value+","+strings.Join(derived, ","))
		if c.Copies == 2 && r.Role == "insert" {
			rf, err := findFlags(live, "replicationFactor", "storageNode")
			if err != nil {
				return nil, fmt.Errorf("%s: %w", what, err)
			}
			switch {
			case len(rf) == 0:
				ops = append(ops, op{"add", h.site.path(h.tok.index), "-replicationFactor=2"})
			case len(rf) == 1 && rf[0].tok.value == "2":
			default:
				return nil, fmt.Errorf("%s: live -replicationFactor %v; two copies set exactly -replicationFactor=2", what, targetValues(rf))
			}
		}
		if c.Copies == 2 && r.Role == "select" {
			dd, err := findFlags(live, dedupName, "storageNode")
			if err != nil {
				return nil, fmt.Errorf("%s: %w", what, err)
			}
			switch {
			case len(dd) == 0:
				ops = append(ops, op{"add", h.site.path(h.tok.index), dedupExact})
			case len(dd) == 1 && dd[0].tok.value == "1ms":
			default:
				return nil, fmt.Errorf("%s: live -%s %v; two copies need exactly 1ms on vmselect", what, dedupName, targetValues(dd))
			}
		}
		return ops, nil
	}
}

// addDedup (VictoriaMetrics two copies, existing vmstorage): one exact
// -dedup.minScrapeInterval=1ms at the front of its only container's args. A live
// vmstorage that already has exactly 1ms needs no change; any other value is refused.
func addDedup(w string) builder {
	return func(live map[string]any) ([]op, error) {
		what := "storage workload " + w
		hits, err := findFlags(live, dedupName)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		if len(hits) == 1 && hits[0].tok.value == "1ms" {
			return nil, nil
		}
		if len(hits) > 0 {
			return nil, fmt.Errorf("%s: live -%s %v; two copies need exactly 1ms", what, dedupName, targetValues(hits))
		}
		containers, _ := mapAt(live, "spec", "template", "spec")["containers"].([]any)
		if len(containers) != 1 {
			return nil, fmt.Errorf("%s: %d containers; obs adds -%s only to a single-container vmstorage", what, len(containers), dedupName)
		}
		cm, _ := containers[0].(map[string]any)
		name, _ := cm["name"].(string)
		args, ok := cm["args"].([]any)
		if command, _ := cm["command"].([]any); !ok || len(args) == 0 || len(command) > 1 {
			return nil, fmt.Errorf("%s: obs inserts -%s only at the front of an existing, nonempty args list (command at most the binary)", what, dedupName)
		}
		return []op{
			{"test", pointer("spec", "template", "spec", "containers", 0, "name"), name},
			{"test", pointer("spec", "template", "spec", "containers", 0, "args", 0), args[0]},
			{"add", pointer("spec", "template", "spec", "containers", 0, "args", 0), dedupExact},
		}, nil
	}
}

// templateRoles sets the role labels of a new workload's pod template: assigned roles
// true, the others false; each present value pinned. Nil when already so.
func templateRoles(live map[string]any, roles, assigned []string) []op {
	tpl := stringMap(mapAt(live, "spec", "template", "metadata")["labels"])
	active := map[string]bool{}
	for _, r := range assigned {
		active[r] = true
	}
	var ops []op
	for _, role := range roles {
		want := "false"
		if active[role] {
			want = "true"
		}
		v, ok := tpl[role]
		switch {
		case ok && v == want:
		case ok:
			ops = append(ops, op{"test", rolePath(role), v}, op{"replace", rolePath(role), want})
		default:
			ops = append(ops, op{"add", rolePath(role), want})
		}
	}
	return ops
}
