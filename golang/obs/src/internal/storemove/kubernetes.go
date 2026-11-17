package storemove

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Command is injectable: unit tests run a fake kubectl and never contact a cluster.
type Command func(context.Context, ...string) ([]byte, error)
type Kubernetes struct {
	Command Command
	Timeout time.Duration
}

func (k Kubernetes) command(ctx context.Context, c Config, args ...string) ([]byte, error) {
	all := append([]string{"--context", c.Context, "--namespace", c.Namespace}, args...)
	if k.Command != nil {
		return k.Command(ctx, all...)
	}
	b, err := exec.CommandContext(ctx, "kubectl", all...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl %v: %w: %s", args, err, b)
	}
	return b, nil
}
func (k Kubernetes) call(ctx context.Context, c Config, args ...string) error {
	_, err := k.command(ctx, c, args...)
	return err
}

// get reads one object; found is false when kubectl prints nothing (--ignore-not-found).
func (k Kubernetes) get(ctx context.Context, c Config, resource string) (map[string]any, bool, error) {
	b, err := k.command(ctx, c, "get", qualified(resource), "-o", "json", "--ignore-not-found")
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, false, nil
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, false, fmt.Errorf("live %s: %w", resource, err)
	}
	return m, true, nil
}
func (k Kubernetes) mustGet(ctx context.Context, c Config, resource string) (map[string]any, error) {
	m, found, err := k.get(ctx, c, resource)
	if err == nil && !found {
		err = fmt.Errorf("%s does not exist live", resource)
	}
	return m, err
}

// pods lists the pods a selector matches.
func (k Kubernetes) pods(ctx context.Context, c Config, selector map[string]string) ([]map[string]any, error) {
	b, err := k.command(ctx, c, "get", "pods", "-l", selectorString(selector), "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err = json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("pods %s: %w", selectorString(selector), err)
	}
	return list.Items, nil
}

func (k Kubernetes) patch(ctx context.Context, c Config, resource string, ops []op) error {
	b, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	return k.call(ctx, c, "patch", qualified(resource), "--type=json", "-p", string(b))
}

// workloadStatus is the controller-specific replica picture: Deployment and StatefulSet
// report spec.replicas/status.readyReplicas, a DaemonSet (the concept 10.3 multi-node
// Alloy form) reports desiredNumberScheduled/numberReady/updatedNumberScheduled.
type workloadStatus struct {
	kind                   string
	desired, ready, update int
}

func (k Kubernetes) status(ctx context.Context, c Config, w string) (workloadStatus, error) {
	b, err := k.command(ctx, c, "get", qualified(w), "-o", "json")
	if err != nil {
		return workloadStatus{}, err
	}
	var obj struct {
		Kind string
		Spec struct {
			Replicas *int `json:"replicas"`
		}
		Status struct {
			Ready     int `json:"readyReplicas"`
			Updated   int `json:"updatedReplicas"`
			Desired   int `json:"desiredNumberScheduled"`
			NumReady  int `json:"numberReady"`
			Scheduled int `json:"updatedNumberScheduled"`
		}
	}
	if err = json.Unmarshal(b, &obj); err != nil {
		return workloadStatus{}, fmt.Errorf("%s: %w", w, err)
	}
	kind, _, _ := strings.Cut(canonicalResource(w), "/")
	switch kind {
	case "daemonset":
		return workloadStatus{kind, obj.Status.Desired, obj.Status.NumReady, obj.Status.Scheduled}, nil
	case "deployment", "statefulset":
		if obj.Spec.Replicas == nil {
			return workloadStatus{kind, 0, obj.Status.Ready, 0}, nil
		}
		return workloadStatus{kind, *obj.Spec.Replicas, obj.Status.Ready, *obj.Spec.Replicas}, nil
	}
	return workloadStatus{}, fmt.Errorf("%s: unsupported workload kind %q (want deployment, statefulset or daemonset)", w, kind)
}
func (k Kubernetes) ready(ctx context.Context, c Config, resources []string) error {
	timeout := k.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	for _, w := range resources {
		if err := k.call(ctx, c, "rollout", "status", qualified(w), "--timeout="+timeout.String()); err != nil {
			return err
		}
		st, err := k.status(ctx, c, w)
		if err != nil {
			return err
		}
		if st.desired < 1 || st.ready < st.desired || st.update < st.desired {
			return fmt.Errorf("%s: want all nonzero desired replicas Ready and updated (%s desired %d, ready %d, updated %d)", w, st.kind, st.desired, st.ready, st.update)
		}
	}
	return nil
}
func (k Kubernetes) alloy(ctx context.Context, c Config) error {
	st, err := k.status(ctx, c, c.Alloy)
	if err != nil {
		return err
	}
	if st.desired < 2 {
		return fmt.Errorf("%s: Alloy must have at least 2 replicas (DaemonSet: 2 scheduled nodes), got %d", c.Alloy, st.desired)
	}
	return nil
}

// change applies the change build makes to an existing object, with its inverse. When
// the object already carries an inverse (an earlier run made the change), the inverse is
// verified and nothing is applied: the step resumes.
func (k Kubernetes) change(ctx context.Context, c Config, resource string, build builder) error {
	live, err := k.mustGet(ctx, c, resource)
	if err != nil {
		return err
	}
	if stored, err := storedInverse(live); err != nil || stored != nil {
		if err == nil {
			err = verifyInverse(live, stored, build)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", resource, err)
		}
		return nil
	}
	fwd, err := build(live)
	if err != nil || len(fwd) == 0 {
		return err
	}
	full, _, err := withInverse(live, fwd)
	if err != nil {
		return fmt.Errorf("%s: %w", resource, err)
	}
	return k.patch(ctx, c, resource, full)
}

// undo applies the verified stored inverse of an object; false when it has none.
func (k Kubernetes) undo(ctx context.Context, c Config, resource string, build builder) (bool, error) {
	live, err := k.mustGet(ctx, c, resource)
	if err != nil {
		return false, err
	}
	stored, err := storedInverse(live)
	if err == nil && stored != nil {
		err = verifyInverse(live, stored, build)
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", resource, err)
	}
	if stored == nil {
		return false, nil
	}
	return true, k.patch(ctx, c, resource, stored)
}

// verifyOrBuild is the read-only form of change: true when the object carries a
// verified inverse (the change is live); otherwise, with build set, the change must be
// buildable from the live object now (so a refusal surfaces before the first change).
func (k Kubernetes) verifyOrBuild(ctx context.Context, c Config, resource string, b builder, build bool) (bool, error) {
	live, err := k.mustGet(ctx, c, resource)
	if err != nil {
		return false, err
	}
	stored, err := storedInverse(live)
	if err == nil && stored != nil {
		err = verifyInverse(live, stored, b)
	} else if err == nil && build {
		_, err = b(live)
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", resource, err)
	}
	return stored != nil, nil
}

// scaledState reads every object of the scaled manifest live. An object is absent, or
// present with the owner label and the digest of exactly this manifest object; anything
// else is refused. For every absent workload, its claims (volumeClaimTemplates x
// replicas) and any pod its selector would adopt must be absent too.
func (k Kubernetes) scaledState(ctx context.Context, c Config, store string, objs []object) (absent []object, err error) {
	for _, o := range objs {
		l, found, err := k.get(ctx, c, o.key())
		if err != nil {
			return nil, err
		}
		if !found {
			for _, claim := range claimNames(o) {
				_, exists, err := k.get(ctx, c, "persistentvolumeclaim/"+claim)
				if err != nil {
					return nil, err
				}
				if exists {
					return nil, fmt.Errorf("%s: %s would bind the existing claim %s (volumeClaimTemplate-statefulset-ordinal); refusing to adopt another store's data", c.ScaledManifest, o.key(), claim)
				}
			}
			if o.Kind == "StatefulSet" || o.Kind == "Deployment" {
				sel, _ := selectorOf(o.Raw, o.key(), c.RoleLabels)
				ps, err := k.pods(ctx, c, sel)
				if err != nil {
					return nil, err
				}
				if len(ps) > 0 {
					return nil, fmt.Errorf("%s: %s selector %s already matches %d live pods; a new workload must not adopt existing pods", c.ScaledManifest, o.key(), selectorString(sel), len(ps))
				}
			}
			absent = append(absent, o)
			continue
		}
		meta := mapAt(l, "metadata")
		if stringMap(meta["labels"])[ownerLabel] != store || stringMap(meta["annotations"])[digestAnnotation] != digest(o) {
			return nil, fmt.Errorf("%s: %s exists live and was not created by obs store move from this manifest (label %s=%s and annotation %s=<digest of the manifest object>); the scaled manifest only creates new objects", c.ScaledManifest, o.key(), ownerLabel, store, digestAnnotation)
		}
	}
	return absent, nil
}

// singleBuilder is the forward change of the single: stopped (Tempo) or retained as the
// metastore (Pyroscope).
func singleBuilder(store string, c Config) builder {
	if store == "pyroscope" {
		return retainMetastore(c)
	}
	return stopSingle(c)
}

func roleWorkloads(c Config) []string {
	var ws []string
	for w := range c.RoleWorkloads {
		ws = append(ws, w)
	}
	sort.Strings(ws)
	return ws
}

// Check runs every static and live check before the first change and returns the index
// of the plan step to start at: 0 for a fresh move, later when the live state shows the
// earlier steps' effects (a re-run resumes; it never redoes Prepare on serving pods). Any
// state the move cannot have produced is refused.
func (k Kubernetes) Check(ctx context.Context, store string, c Config, rollback bool) (int, error) {
	c = c.normalized()
	objs, err := validateScaledManifest(c, store)
	if err != nil {
		return 0, err
	}
	absent, err := k.scaledState(ctx, c, store, objs)
	if err != nil {
		return 0, err
	}
	if storage(store) {
		return k.checkStorage(ctx, store, c, rollback, objs, absent)
	}
	return k.checkSingle(ctx, store, c, rollback, objs, absent)
}

func (k Kubernetes) checkSingle(ctx context.Context, store string, c Config, rollback bool, objs, absent []object) (int, error) {
	single, err := k.mustGet(ctx, c, c.Single)
	if err != nil {
		return 0, err
	}
	singleSel, err := selectorOf(single, c.Single, c.RoleLabels)
	if err != nil {
		return 0, err
	}
	for _, o := range objs {
		if o.Kind == "StatefulSet" || o.Kind == "Deployment" {
			sel, _ := selectorOf(o.Raw, o.key(), c.RoleLabels)
			if !disjoint(sel, singleSel) {
				return 0, fmt.Errorf("%s and %s pod selectors may overlap; require distinct component labels", c.Single, o.key())
			}
		}
	}
	// The stable Services must select the role labels the flip changes.
	selected := map[string]bool{}
	for _, name := range stableServices(store) {
		svc, err := k.mustGet(ctx, c, "service/"+name)
		if err != nil {
			return 0, fmt.Errorf("stable Service: %w", err)
		}
		sel := stringMap(mapAt(svc, "spec")["selector"])
		n := 0
		for _, role := range c.RoleLabels {
			if sel[role] == "true" {
				selected[role], n = true, n+1
			}
		}
		if n != 1 {
			return 0, fmt.Errorf("stable Service %s selects %d of the role labels %v with value true, want exactly one", name, n, c.RoleLabels)
		}
	}
	for _, role := range c.RoleLabels {
		if !selected[role] {
			return 0, fmt.Errorf("role label %s is selected by none of the stable Services %v", role, stableServices(store))
		}
	}
	if err = k.alloy(ctx, c); err != nil {
		return 0, err
	}
	stopped, err := k.verifyOrBuild(ctx, c, c.Single, singleBuilder(store, c), !rollback)
	if err != nil {
		return 0, err
	}
	if c.Compactor != "" {
		if _, err = k.verifyOrBuild(ctx, c, c.Compactor, scaleToZero(c.Compactor), !rollback && !stopped); err != nil {
			return 0, err
		}
	}
	// Role state: the scaled templates and the single's pods.
	routed := 0
	for _, w := range roleWorkloads(c) {
		live, found, err := k.get(ctx, c, w)
		if err != nil {
			return 0, err
		}
		if found && len(templateRoles(live, c.RoleLabels, c.RoleWorkloads[w])) == 0 {
			routed++
		}
	}
	pods, err := k.pods(ctx, c, singleSel)
	if err != nil {
		return 0, err
	}
	singleTrue, singleFalse := 0, 0
	for _, p := range pods {
		l := stringMap(mapAt(p, "metadata")["labels"])
		all := true
		for _, role := range c.RoleLabels {
			all = all && l[role] == "true"
		}
		if all {
			singleTrue++
		} else {
			singleFalse++
		}
	}
	if rollback {
		if !stopped && len(absent) == len(objs) {
			return 0, fmt.Errorf("nothing to roll back: no object of %s exists live and %s carries no change by obs", c.ScaledManifest, c.Single)
		}
		if !stopped {
			if n, _ := intField(mapAt(single, "spec")["replicas"]); n < 1 {
				return 0, fmt.Errorf("%s: live replicas 0 without obs's %s annotation; it was not stopped by obs store move, refusing to guess how to restore it", c.Single, inverseAnnotation)
			}
			if _, err := rolesTo(single, c.RoleLabels, "true", "true", "single "+c.Single); err != nil {
				return 0, fmt.Errorf("%w; the rollback only relabels a running single whose template still has its roles", err)
			}
		}
		return 0, nil
	}
	plan := Plan(store, false)
	at := func(s Step) int {
		for i, p := range plan {
			if p == s {
				return i
			}
		}
		return 0
	}
	switch {
	case stopped:
		if len(absent) > 0 || routed != len(c.RoleWorkloads) || singleTrue > 0 {
			return 0, fmt.Errorf("%s carries obs's change but the scaled form is not fully routed (%d absent objects, %d/%d routed workloads); inspect, then roll back explicitly", c.Single, len(absent), routed, len(c.RoleWorkloads))
		}
		return at(Verify), nil
	case routed == 0 && singleFalse == 0:
		return 0, nil // fresh, or created and not yet routed: Prepare creates what is absent and waits
	case len(absent) > 0:
		return 0, fmt.Errorf("%d scaled objects absent but roles already moved; inspect, then roll back explicitly", len(absent))
	case routed == len(c.RoleWorkloads) && singleTrue == 0:
		return at(Reconnect), nil
	case routed > 0:
		return at(Route), nil
	}
	return 0, fmt.Errorf("%d single pods lost their roles while no scaled workload carries them: the stable Services may be empty; restore the single's pod labels first", singleFalse)
}

func (k Kubernetes) checkStorage(ctx context.Context, store string, c Config, rollback bool, objs, absent []object) (int, error) {
	nodes, err := storageNodes(c, objs)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, r := range c.Routers {
		d, err := k.verifyOrBuild(ctx, c, r.Workload, addStorageNodes(c, r, endpoints(c, nodes, r.Port)), true)
		if err != nil {
			return 0, err
		}
		if d {
			done++
		}
	}
	for _, w := range c.Storage {
		if _, err := k.verifyOrBuild(ctx, c, w, addDedup(w), true); err != nil {
			return 0, err
		}
	}
	if rollback {
		if done == 0 && len(absent) == len(objs) {
			return 0, fmt.Errorf("nothing to roll back: no object of %s exists live and no router carries a change by obs", c.ScaledManifest)
		}
		return 0, nil
	}
	switch {
	case done == 0:
		return 0, nil
	case len(absent) > 0:
		return 0, fmt.Errorf("routers already list new storage nodes but %d new objects are absent; inspect, then roll back explicitly", len(absent))
	case done == len(c.Routers):
		return 2, nil // Writes
	}
	return 1, nil // StorageRoute
}

// roles moves the role labels (make before break). Forward: each scaled role workload's
// pods and template get their roles, all of them Ready, then the single's pods lose
// theirs. Reverse: the single's pods get theirs back, then the scaled pods and templates
// lose theirs.
func (k Kubernetes) roles(ctx context.Context, c Config, reverse bool) error {
	single, err := k.mustGet(ctx, c, c.Single)
	if err != nil {
		return err
	}
	singleSel, err := selectorOf(single, c.Single, c.RoleLabels)
	if err != nil {
		return err
	}
	workloads := roleWorkloads(c)
	selectors := map[string]map[string]string{}
	for _, w := range workloads {
		live, err := k.mustGet(ctx, c, w)
		if err != nil {
			return err
		}
		if selectors[w], err = selectorOf(live, w, c.RoleLabels); err != nil {
			return err
		}
		if !disjoint(singleSel, selectors[w]) {
			return fmt.Errorf("%s and %s pod selectors may overlap; require distinct component labels", c.Single, w)
		}
	}
	label := func(sel map[string]string, roles []string, value string) error {
		argv := []string{"label", "pods", "-l", selectorString(sel), "--overwrite"}
		for _, role := range roles {
			argv = append(argv, role+"="+value)
		}
		return k.call(ctx, c, argv...)
	}
	setTemplate := func(w string, assigned []string) error {
		live, err := k.mustGet(ctx, c, w)
		if err != nil {
			return err
		}
		if ops := templateRoles(live, c.RoleLabels, assigned); len(ops) > 0 {
			return k.patch(ctx, c, w, ops)
		}
		return nil
	}
	if !reverse {
		for _, w := range workloads {
			if err = label(selectors[w], c.RoleWorkloads[w], "true"); err != nil {
				return err
			}
			if err = setTemplate(w, c.RoleWorkloads[w]); err != nil {
				return err
			}
		}
		if err = k.ready(ctx, c, workloads); err != nil {
			return err
		}
		// The single's template keeps its roles until the drain stops it in one patch.
		return label(singleSel, c.RoleLabels, "false")
	}
	if err = label(singleSel, c.RoleLabels, "true"); err != nil {
		return err
	}
	if err = k.ready(ctx, c, []string{c.Single}); err != nil {
		return err
	}
	for _, w := range workloads {
		if err = label(selectors[w], c.RoleWorkloads[w], "false"); err != nil {
			return err
		}
		if err = setTemplate(w, nil); err != nil {
			return err
		}
	}
	return k.ready(ctx, c, workloads)
}

func (k Kubernetes) compaction(ctx context.Context, c Config, resume bool) error {
	if c.Compactor != "" {
		if resume {
			_, err := k.undo(ctx, c, c.Compactor, scaleToZero(c.Compactor))
			return err
		}
		return k.change(ctx, c, c.Compactor, scaleToZero(c.Compactor))
	}
	args := c.StopCompaction
	if resume {
		args = c.ResumeCompaction
	}
	if len(args) == 0 {
		return fmt.Errorf("Tempo compaction control argv missing")
	}
	argv := append([]string{"exec", qualified(c.Single), "--"}, args...)
	return k.call(ctx, c, argv...)
}

func (k Kubernetes) Execute(ctx context.Context, s Step, store string, c Config) error {
	c = c.normalized()
	switch s {
	case Prepare, AddNodes:
		objs, err := validateScaledManifest(c, store)
		if err != nil {
			return err
		}
		absent, err := k.scaledState(ctx, c, store, objs)
		if err != nil {
			return err
		}
		if len(absent) > 0 {
			file, err := createFile(c, store, absent)
			if err != nil {
				return err
			}
			defer os.Remove(file)
			if err = k.call(ctx, c, "create", "-f", file); err != nil {
				return err
			}
		}
		return k.ready(ctx, c, c.Scaled)
	case Route:
		return k.roles(ctx, c, false)
	case Reconnect, ReverseReconnect:
		if err := k.alloy(ctx, c); err != nil {
			return err
		}
		if err := k.call(ctx, c, "rollout", "restart", qualified(c.Alloy)); err != nil {
			return err
		}
		return k.ready(ctx, c, []string{c.Alloy})
	case StopCompaction:
		return k.compaction(ctx, c, false)
	case StopSingle:
		return k.change(ctx, c, c.Single, stopSingle(c))
	case RetainMetastore:
		if err := k.change(ctx, c, c.Single, retainMetastore(c)); err != nil {
			return err
		}
		return k.ready(ctx, c, []string{c.Single})
	case Restore:
		// Read the live single first: a single obs stopped or converted comes back through
		// its verified inverse (roles and replicas in one patch, so the first Ready pod
		// already has its roles); a running single is never scaled down, only relabelled
		// in ReverseRoles.
		if _, err := k.undo(ctx, c, c.Single, singleBuilder(store, c)); err != nil {
			return err
		}
		if err := k.ready(ctx, c, []string{c.Single}); err != nil {
			return err
		}
		if store == "tempo" {
			return k.compaction(ctx, c, true)
		}
		return nil
	case ReverseRoles:
		return k.roles(ctx, c, true)
	case StorageRoute, RestoreRoute:
		objs, err := validateScaledManifest(c, store)
		if err != nil {
			return err
		}
		nodes, err := storageNodes(c, objs)
		if err != nil {
			return err
		}
		var routers []string
		for _, r := range c.Routers {
			routers = append(routers, r.Workload)
		}
		if s == StorageRoute {
			// Storage side first (two copies: dedup on every vmstorage), then the routers.
			for _, w := range c.Storage {
				if err := k.change(ctx, c, w, addDedup(w)); err != nil {
					return err
				}
			}
			if err := k.ready(ctx, c, c.Storage); err != nil {
				return err
			}
			for _, r := range c.Routers {
				if err := k.change(ctx, c, r.Workload, addStorageNodes(c, r, endpoints(c, nodes, r.Port))); err != nil {
					return err
				}
			}
			return k.ready(ctx, c, routers)
		}
		for _, r := range c.Routers {
			if _, err := k.undo(ctx, c, r.Workload, addStorageNodes(c, r, endpoints(c, nodes, r.Port))); err != nil {
				return err
			}
		}
		if err := k.ready(ctx, c, routers); err != nil {
			return err
		}
		for _, w := range c.Storage {
			if _, err := k.undo(ctx, c, w, addDedup(w)); err != nil {
				return err
			}
		}
		return k.ready(ctx, c, c.Storage)
	default:
		return fmt.Errorf("unsupported cluster step %q", s)
	}
}
