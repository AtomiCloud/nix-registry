package storemove

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Every manifest the move applies is checked against the live object it would change
// (kubectl get -o json through the injectable Command; tests use a fake) before the
// first mutating call:
//   - scaled_manifest: each object is new (absent live, then it must carry the owner
//     label) or was created by the move (owner label live); an existing object without
//     the label is refused, except a VictoriaLogs/VictoriaMetrics storage workload whose
//     only change is more replicas (adding storage nodes, concept 10.6).
//   - single_manifest / metastore_manifest: the single must exist live; the manifest may
//     differ from it only in the declared conversion fields (Pyroscope -target, the role
//     labels of the pod template; for the rollback also spec.replicas, which is applied as
//     0 and scaled up after the roles).
//   - routing manifests: the file describing the current state (original before the
//     move, forward before the rollback; the other one when a step is re-run) must match
//     the live router/storage workload, so the only change applied is the reviewed one.
// "Match" is two containments: manifest within live (every field it sets has the live
// value) and the live last-applied-configuration within the manifest (kubectl apply
// deletes a previously applied field the manifest omits).

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// live reads one object; found is false when kubectl prints nothing (--ignore-not-found).
func (k Kubernetes) live(ctx context.Context, c Config, o object) (map[string]any, bool, error) {
	b, err := k.command(ctx, c, "get", kubectlResource(o), "-o", "json", "--ignore-not-found")
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, false, nil
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, false, fmt.Errorf("live %s: %w", kubectlResource(o), err)
	}
	return m, true, nil
}

// lastApplied returns the live object's kubectl last-applied-configuration (nil when the
// object was never client-side applied, for example Helm-installed).
func lastApplied(live map[string]any, what string) (map[string]any, error) {
	ann, _ := mapAt(live, "metadata", "annotations")[lastAppliedAnnotation].(string)
	if ann == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(ann), &m); err != nil {
		return nil, fmt.Errorf("live %s: unreadable %s: %w", what, lastAppliedAnnotation, err)
	}
	return m, nil
}

func ownerOf(m map[string]any) string {
	return stringMap(mapAt(m, "metadata")["labels"])[ownerLabel]
}

// matchLive checks manifest m against live object l (and its last-applied-configuration).
func matchLive(m, l map[string]any, what string, skip func(string) bool) error {
	if err := within(m, l, "", skip); err != nil {
		return fmt.Errorf("differs from the live %s at %v (manifest vs live)", what, err)
	}
	last, err := lastApplied(l, what)
	if err != nil {
		return err
	}
	if last != nil {
		lastSkip := func(p string) bool { return p == "metadata.namespace" || skip != nil && skip(p) }
		if err := within(last, m, "", lastSkip); err != nil {
			return fmt.Errorf("omits or changes a field the live %s's last-applied-configuration sets, which kubectl apply would delete or change: %v (last-applied vs manifest)", what, err)
		}
	}
	return nil
}

func (k Kubernetes) liveScaled(ctx context.Context, c Config, store string, objs []object) error {
	scaled := map[string]bool{}
	for _, w := range c.Scaled {
		scaled[w] = true
	}
	for _, o := range objs {
		l, found, err := k.live(ctx, c, o)
		if err != nil {
			return err
		}
		own := ownerOf(o.Raw)
		switch {
		case !found:
			if own != store {
				return fmt.Errorf("%s: new %s/%s must carry label %s: %s, so a later run can tell it from objects the move does not own", c.ScaledManifest, o.Kind, o.Name, ownerLabel, store)
			}
		case ownerOf(l) == store:
			if own != store {
				return fmt.Errorf("%s: %s/%s was created by the move (label %s: %s live); the manifest must keep that label", c.ScaledManifest, o.Kind, o.Name, ownerLabel, store)
			}
		case storage(store) && (o.Kind == "StatefulSet" || o.Kind == "Deployment") && scaled[o.key()]:
			// Adding storage nodes to the existing cluster-of-one storage workload:
			// only more replicas.
			skip := func(p string) bool { return p == "spec.replicas" }
			if err = matchLive(o.Raw, l, o.key(), skip); err != nil {
				return fmt.Errorf("%s: existing storage workload %s may only gain replicas; it %w", c.ScaledManifest, o.key(), err)
			}
			want, _ := mapAt(o.Raw, "spec")["replicas"].(int)
			have, _ := mapAt(l, "spec")["replicas"].(float64)
			if float64(want) < have {
				return fmt.Errorf("%s: existing storage workload %s replicas %d < live %g; removing storage nodes is not part of the move", c.ScaledManifest, o.key(), want, have)
			}
		default:
			return fmt.Errorf("%s: %s/%s exists live and was not created by obs store move (no label %s: %s); refusing to overwrite it", c.ScaledManifest, o.Kind, o.Name, ownerLabel, store)
		}
	}
	return nil
}

const targetSentinel = "<target>"

// normalizeTarget replaces the value of every -target flag (any well-formed spelling,
// spelling kept) by a sentinel. With want set, every value must equal want first.
func normalizeTarget(o map[string]any, want, what string) error {
	return argSites(o, func(site string, container map[string]any, field string, args []any) error {
		tokens, err := tokenize(args, field == "command", "target")
		if err != nil {
			return fmt.Errorf("%s %s: %w", what, site, err)
		}
		for _, t := range tokens {
			if t.name != "target" {
				continue
			}
			if want != "" && t.value != want {
				return fmt.Errorf("%s %s: -target=%s, want -target=%s", what, site, t.value, want)
			}
			if t.consume {
				args[t.index+1] = targetSentinel
			} else {
				args[t.index] = t.spelling() + targetSentinel
			}
		}
		return nil
	})
}

// liveSingle checks single_manifest (metastore=false) or metastore_manifest against the
// live single. Allowed differences: Pyroscope's -target value (all -> metastore when
// retained, metastore/all -> all when restored), the role labels of the pod template
// (never true here, see validateIsolatedObjects) and, for the rollback only,
// spec.replicas (applied as 0, then scaled up after the roles). Everything else (the
// replica count of the retained metastore, its PVC retention policy, volumeClaimTemplates,
// volume mounts and data path, raft node id/advertise address, image, env, ...) must
// equal the live single.
func (k Kubernetes) liveSingle(ctx context.Context, c Config, store, file string, single object, metastore bool) error {
	l, found, err := k.live(ctx, c, single)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s: the single %s does not exist live; the move converts or restores the existing single and never recreates it", file, c.Single)
	}
	// The rollback applies the single at replicas 0 (and the Tempo drain scales it to 0):
	// a live Delete retention policy would delete its claims then, even when the
	// manifest leaves the policy alone.
	if p := mapAt(l, "spec", "persistentVolumeClaimRetentionPolicy"); p != nil {
		for _, k := range sortedKeys(p) {
			if scalar(p[k]) == "Delete" {
				return fmt.Errorf("%s: the live single %s has persistentVolumeClaimRetentionPolicy.%s=Delete; scaling it to 0 would delete its claims; set Retain on the live object first", file, c.Single, k)
			}
		}
	}
	m, _ := deepCopy(single.Raw).(map[string]any)
	l, _ = deepCopy(l).(map[string]any)
	if store == "pyroscope" {
		want := "all"
		if metastore {
			want = "metastore"
		}
		if err = normalizeTarget(m, want, file); err != nil {
			return err
		}
		if err = normalizeTarget(l, "", "live "+c.Single); err != nil {
			return err
		}
		last, e := lastApplied(l, c.Single)
		if e != nil {
			return e
		}
		if last != nil {
			if err = normalizeTarget(last, "", "last-applied "+c.Single); err != nil {
				return err
			}
			b, _ := json.Marshal(last)
			mapAt(l, "metadata", "annotations")[lastAppliedAnnotation] = string(b)
		}
	}
	skip := func(p string) bool {
		if !metastore && p == "spec.replicas" {
			return true
		}
		for _, role := range c.RoleLabels {
			if p == "spec.template.metadata.labels."+role {
				return true
			}
		}
		return false
	}
	if err = matchLive(m, l, c.Single, skip); err != nil {
		allowed := "the pod template role labels"
		if store == "pyroscope" {
			allowed += " and -target"
		}
		if !metastore {
			allowed += " and spec.replicas"
		}
		return fmt.Errorf("%s: %s %w; only %s may change", file, c.Single, err, allowed)
	}
	return nil
}

// liveRouting checks each router/storage workload of the routing manifests against the
// live object before either file is applied.
func (k Kubernetes) liveRouting(ctx context.Context, c Config, s Step, fwd, orig map[string]object) error {
	current, currentFile, other, otherFile := orig, c.OriginalRoutingManifest, fwd, c.RoutingManifest
	if s == RestoreRoute {
		current, currentFile, other, otherFile = fwd, c.RoutingManifest, orig, c.OriginalRoutingManifest
	}
	for _, key := range sortedKeys(current) {
		l, found, err := k.live(ctx, c, current[key])
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%s: %s does not exist live; routing manifests change existing routers/storage only", currentFile, key)
		}
		err = matchLive(current[key].Raw, l, key, nil)
		if err == nil {
			continue
		}
		// A re-run after a partial apply: the object is already in the other state.
		if matchLive(other[key].Raw, l, key, nil) == nil {
			continue
		}
		return fmt.Errorf("%s: %s %w (and does not match %s either); the manifests must describe the live object", currentFile, key, err, otherFile)
	}
	return nil
}
