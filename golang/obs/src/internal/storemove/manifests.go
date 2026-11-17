package storemove

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func canonicalKind(kind string) string {
	switch strings.ToLower(kind) {
	case "sts", "statefulset", "statefulsets":
		return "statefulset"
	case "deploy", "deployment", "deployments":
		return "deployment"
	case "ds", "daemonset", "daemonsets":
		return "daemonset"
	case "svc", "service", "services":
		return "service"
	case "pvc", "persistentvolumeclaim", "persistentvolumeclaims":
		return "persistentvolumeclaim"
	case "sa", "serviceaccount", "serviceaccounts":
		return "serviceaccount"
	case "cm", "configmap", "configmaps":
		return "configmap"
	case "endpoints", "ep":
		return "endpoints"
	case "endpointslice", "endpointslices":
		return "endpointslice"
	}
	return strings.ToLower(kind)
}
func canonicalResource(resource string) string {
	kind, name, ok := strings.Cut(resource, "/")
	if !ok {
		return resource
	}
	return canonicalKind(kind) + "/" + name
}
func (c Config) normalized() Config {
	c.Single = canonicalResource(c.Single)
	c.Alloy = canonicalResource(c.Alloy)
	c.Compactor = canonicalResource(c.Compactor)
	c.Scaled = append([]string(nil), c.Scaled...)
	c.Routers = append([]string(nil), c.Routers...)
	c.Storage = append([]string(nil), c.Storage...)
	for i := range c.Scaled {
		c.Scaled[i] = canonicalResource(c.Scaled[i])
	}
	for i := range c.Storage {
		c.Storage[i] = canonicalResource(c.Storage[i])
	}
	for i := range c.Routers {
		c.Routers[i] = canonicalResource(c.Routers[i])
	}
	roles := map[string][]string{}
	for w, labels := range c.RoleWorkloads {
		roles[canonicalResource(w)] = labels
	}
	c.RoleWorkloads = roles
	return c
}

// ownerLabel marks an object the store move itself created (scaled_manifest). A live
// object without it is never overwritten (except the VictoriaLogs/VictoriaMetrics
// storage StatefulSet gaining replicas, see liveScaled).
const ownerLabel = "obs.diene/store-move"

func stableService(name string) bool {
	switch name {
	case "vl-insert", "vl-select", "tempo-distributor", "tempo-query", "vm-insert", "vm-select", "pyroscope-write", "pyroscope-query":
		return true
	}
	return false
}
func stringMap(v any) map[string]string {
	m, _ := v.(map[string]any)
	out := map[string]string{}
	for k, x := range m {
		out[k] = scalar(x)
	}
	return out
}
func protectedObject(o object, c Config) bool {
	kind := canonicalKind(o.Kind)
	labels := stringMap(mapAt(o.Raw, "metadata")["labels"])
	if kind == "service" && stableService(o.Name) {
		return true
	}
	if kind == "endpoints" || kind == "endpointslice" {
		if stableService(o.Name) || stableService(labels["kubernetes.io/service-name"]) {
			return true
		}
		owners, _ := mapAt(o.Raw, "metadata")["ownerReferences"].([]any)
		for _, owner := range owners {
			om, _ := owner.(map[string]any)
			k, _ := om["kind"].(string)
			n, _ := om["name"].(string)
			if canonicalKind(k) == "service" && stableService(n) {
				return true
			}
		}
	}
	if kind == "configmap" || kind == "secret" {
		_, alloyName, _ := strings.Cut(c.Alloy, "/")
		if o.Name == "alloy" || strings.HasPrefix(o.Name, "alloy-") || alloyName != "" && o.Name == alloyName || labels["app.kubernetes.io/name"] == "alloy" {
			return true
		}
		for _, r := range c.AlloyConfigObjects {
			if canonicalResource(r) == kind+"/"+o.Name {
				return true
			}
		}
	}
	return false
}
func validateProtectedObjects(file string, objs []object, c Config) error {
	for _, o := range objs {
		if protectedObject(o, c) {
			return fmt.Errorf("%s: protected stable Service/endpoint or Alloy config %s/%s must not change", file, o.Kind, o.Name)
		}
	}
	return nil
}
func validateProtectedManifest(file string, c Config) error {
	objs, err := parseManifest(file, c.Namespace)
	if err != nil {
		return err
	}
	return validateProtectedObjects(file, objs, c)
}
func validateIsolatedObjects(file string, objs []object, c Config) error {
	for _, o := range objs {
		tpl := stringMap(mapAt(o.Raw, "spec", "template", "metadata")["labels"])
		own := stringMap(mapAt(o.Raw, "metadata")["labels"])
		for _, role := range c.RoleLabels {
			if tpl[role] == "true" || own[role] == "true" {
				return fmt.Errorf("%s: %s/%s role %s=true routes before the role step", file, o.Kind, o.Name, role)
			}
		}
	}
	return nil
}

// ownNames are the names a supporting object of the scaled form may carry: a scaled
// workload, the single or a declared storage workload, alone or as a "<name>-" prefix
// (tempo-live-store-headless, pyroscope-metastore, redpanda-config). Everything else,
// such as an app's checkout-env or checkout Service, is not the store's.
func ownNames(c Config) []string {
	var names []string
	for _, r := range append(append(append([]string(nil), c.Scaled...), c.Storage...), c.Single) {
		if _, n, ok := strings.Cut(r, "/"); ok && n != "" {
			names = append(names, n)
		}
	}
	return names
}
func ownName(name string, c Config) bool {
	for _, n := range ownNames(c) {
		if name == n || strings.HasPrefix(name, n+"-") {
			return true
		}
	}
	return false
}
func stringList(v any) ([]string, bool) {
	l, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
func subsetOf(list []string, allowed ...string) bool {
	for _, s := range list {
		ok := false
		for _, a := range allowed {
			ok = ok || s == a
		}
		if !ok {
			return false
		}
	}
	return true
}

// validateSupporting is the per-kind allow-list for the non-workload objects of the
// scaled form (concept 10.5): headless Services (Tempo: publishNotReadyAddresses true),
// the scaled form's own ConfigMap, and for Pyroscope only the ServiceAccount, Role and
// RoleBinding that kubernetes:/// metastore discovery needs (read pods/endpoints only).
func validateSupporting(file string, o object, objs []object, store string, c Config) error {
	if !ownName(o.Name, c) {
		return fmt.Errorf("%s: %s/%s is not one of the store's own names (a scaled workload, the single or a storage workload, or <name>-...: %s)", file, o.Kind, o.Name, strings.Join(ownNames(c), ", "))
	}
	spec := mapAt(o.Raw, "spec")
	switch o.Kind {
	case "Service":
		if spec == nil || spec["clusterIP"] != "None" {
			return fmt.Errorf("%s: Service/%s must be headless (spec.clusterIP: None); concept 10.5 adds only headless Services", file, o.Name)
		}
		for _, k := range sortedKeys(spec) {
			switch k {
			case "clusterIP", "clusterIPs", "ports", "selector", "publishNotReadyAddresses", "type", "ipFamilies", "ipFamilyPolicy", "internalTrafficPolicy":
			default:
				return fmt.Errorf("%s: Service/%s spec.%s is not allowed on a headless Service", file, o.Name, k)
			}
		}
		if t, ok := spec["type"]; ok && t != "ClusterIP" {
			return fmt.Errorf("%s: Service/%s type %v, want ClusterIP (headless)", file, o.Name, t)
		}
		if store == "tempo" && spec["publishNotReadyAddresses"] != true {
			return fmt.Errorf("%s: Service/%s: Tempo headless Services set publishNotReadyAddresses: true (concept 10.5)", file, o.Name)
		}
	case "ConfigMap":
	case "ServiceAccount", "Role", "RoleBinding":
		if store != "pyroscope" {
			return fmt.Errorf("%s: %s/%s: RBAC objects belong only to the Pyroscope kubernetes:/// metastore form (concept 10.5)", file, o.Kind, o.Name)
		}
		switch o.Kind {
		case "Role":
			rules, _ := o.Raw["rules"].([]any)
			if len(rules) == 0 {
				return fmt.Errorf("%s: Role/%s without rules", file, o.Name)
			}
			for i, r := range rules {
				rm, _ := r.(map[string]any)
				groups, g := stringList(rm["apiGroups"])
				resources, rs := stringList(rm["resources"])
				verbs, v := stringList(rm["verbs"])
				extra := false
				for k := range rm {
					extra = extra || (k != "apiGroups" && k != "resources" && k != "verbs")
				}
				if !g || !rs || !v || extra || !subsetOf(groups, "", "discovery.k8s.io") || !subsetOf(resources, "pods", "endpoints", "endpointslices") || !subsetOf(verbs, "get", "list", "watch") {
					return fmt.Errorf("%s: Role/%s rules[%d]: only get/list/watch on pods, endpoints, endpointslices (core, discovery.k8s.io) are allowed", file, o.Name, i)
				}
			}
		case "RoleBinding":
			ref, _ := o.Raw["roleRef"].(map[string]any)
			refName, _ := ref["name"].(string)
			if ref["kind"] != "Role" || ref["apiGroup"] != "rbac.authorization.k8s.io" || !manifestHas(objs, "Role", refName) {
				return fmt.Errorf("%s: RoleBinding/%s must bind a Role defined in this manifest (got roleRef %v)", file, o.Name, ref)
			}
			subjects, _ := o.Raw["subjects"].([]any)
			if len(subjects) == 0 {
				return fmt.Errorf("%s: RoleBinding/%s without subjects", file, o.Name)
			}
			for i, s := range subjects {
				sm, _ := s.(map[string]any)
				n, _ := sm["name"].(string)
				ns, hasNS := sm["namespace"]
				if sm["kind"] != "ServiceAccount" || !manifestHas(objs, "ServiceAccount", n) || hasNS && ns != c.Namespace {
					return fmt.Errorf("%s: RoleBinding/%s subjects[%d]: only ServiceAccounts defined in this manifest, in namespace %s", file, o.Name, i, c.Namespace)
				}
			}
		}
	default:
		return fmt.Errorf("%s: %s/%s: kind not allowed in scaled_manifest (allowed: StatefulSet, Deployment, headless Service, ConfigMap; Pyroscope also ServiceAccount, Role, RoleBinding)", file, o.Kind, o.Name)
	}
	return nil
}
func manifestHas(objs []object, kind, name string) bool {
	for _, o := range objs {
		if o.Kind == kind && o.Name == name {
			return true
		}
	}
	return false
}

// validateVolumes refuses a new workload that mounts an existing claim or a host path:
// new storage comes from its own volumeClaimTemplates, so a scaled pod can never open
// the single's (or an app's) data directory next to its owner.
func validateVolumes(file string, o object) error {
	vols, _ := mapAt(o.Raw, "spec", "template", "spec")["volumes"].([]any)
	for i, v := range vols {
		vm, _ := v.(map[string]any)
		for _, k := range []string{"persistentVolumeClaim", "hostPath"} {
			if _, ok := vm[k]; ok {
				return fmt.Errorf("%s: %s/%s volumes[%d] uses %s; new workloads get storage only from volumeClaimTemplates", file, o.Kind, o.Name, i, k)
			}
		}
	}
	return nil
}

// validateScaledManifest is the static allow-list of scaled_manifest (no cluster read;
// liveScaled checks existence and ownership afterwards).
func validateScaledManifest(c Config, store string) ([]object, error) {
	c = c.normalized()
	file := c.ScaledManifest
	objs, err := parseManifest(file, c.Namespace)
	if err != nil {
		return nil, err
	}
	if err = validateProtectedObjects(file, objs, c); err != nil {
		return nil, err
	}
	if err = validateIsolatedObjects(file, objs, c); err != nil {
		return nil, err
	}
	scaled := map[string]bool{}
	for _, w := range c.Scaled {
		scaled[w] = true
	}
	seen := map[string]bool{}
	for _, o := range objs {
		key := o.key()
		if key == c.Single || key == c.Alloy {
			return nil, fmt.Errorf("%s: scaled_manifest must not change old single or Alloy %s before routing and drain", file, key)
		}
		switch o.Kind {
		case "StatefulSet", "Deployment":
			if !scaled[key] {
				return nil, fmt.Errorf("%s: workload %s is not listed in scaled; refusing to skip its readiness", file, key)
			}
			r, ok := mapAt(o.Raw, "spec")["replicas"].(int)
			if !ok || r < 1 {
				return nil, fmt.Errorf("%s: %s requires positive explicit replicas", file, key)
			}
			if err = validateVolumes(file, o); err != nil {
				return nil, err
			}
			seen[key] = true
		default:
			if err = validateSupporting(file, o, objs, store, c); err != nil {
				return nil, err
			}
		}
	}
	for _, w := range c.Scaled {
		if !seen[w] {
			return nil, fmt.Errorf("%s: scaled workload %s is missing", file, w)
		}
	}
	return objs, nil
}

// validateSingleManifest is the static allow-list of single_manifest (rollback restore)
// and metastore_manifest (Pyroscope retain): exactly the single, nothing else; the
// field-level check against the live single is liveSingle.
func validateSingleManifest(file string, c Config, metastore bool) (object, error) {
	objs, err := parseManifest(file, c.Namespace)
	if err != nil {
		return object{}, err
	}
	if err = validateProtectedObjects(file, objs, c); err != nil {
		return object{}, err
	}
	if err = validateIsolatedObjects(file, objs, c); err != nil {
		return object{}, err
	}
	var single *object
	for i, o := range objs {
		if o.key() != c.Single {
			return object{}, fmt.Errorf("%s: %s/%s is not the single %s; this manifest may contain only the single (supporting objects of the scaled form belong in scaled_manifest)", file, o.Kind, o.Name, c.Single)
		}
		single = &objs[i]
	}
	if single == nil {
		return object{}, fmt.Errorf("%s: does not contain the single %s", file, c.Single)
	}
	if single.Kind != "StatefulSet" && single.Kind != "Deployment" || metastore && single.Kind != "StatefulSet" {
		return object{}, fmt.Errorf("%s: the single %s must be a StatefulSet (Pyroscope metastore) or Deployment/StatefulSet", file, c.Single)
	}
	spec := mapAt(single.Raw, "spec")
	if metastore {
		r, ok := spec["replicas"].(int)
		if !ok || r < 1 {
			return object{}, fmt.Errorf("%s: metastore %s needs explicit replicas >= 1 (it keeps running as the metastore; 0 would stop it)", file, c.Single)
		}
	}
	if p := mapAt(spec, "persistentVolumeClaimRetentionPolicy"); p != nil {
		for _, k := range sortedKeys(p) {
			if scalar(p[k]) != "Retain" {
				return object{}, fmt.Errorf("%s: %s persistentVolumeClaimRetentionPolicy.%s=%v; the single's claims are always retained", file, c.Single, k, p[k])
			}
		}
	}
	return *single, nil
}

// storageSentinel stands in for a -storageNode flag and its value, at the same position,
// once its endpoints have been read out; the flag's exact spelling is appended, so a
// changed spelling (dashes, inline or separate value) is a difference.
const storageSentinel = "<storageNode>"

// dedupName/dedupExact: the one -dedup.minScrapeInterval setting concept 10.6 documents
// (vmstorage x2, "=1ms"); no other value or spelling is ever accepted as the change.
const (
	dedupName  = "dedup.minScrapeInterval"
	dedupExact = "-dedup.minScrapeInterval=1ms"
)

// argSites calls fn for every args/command list of every container and init container
// of a workload, with a stable site name such as "containers[0](c).args".
func argSites(o map[string]any, fn func(site string, container map[string]any, field string, args []any) error) error {
	pod := mapAt(o, "spec", "template", "spec")
	for _, group := range []string{"initContainers", "containers"} {
		containers, _ := pod[group].([]any)
		for i, c := range containers {
			container, _ := c.(map[string]any)
			name, _ := container["name"].(string)
			for _, field := range []string{"command", "args"} {
				if args, ok := container[field].([]any); ok {
					if err := fn(fmt.Sprintf("%s[%d](%s).%s", group, i, name, field), container, field, args); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func setArgs(container map[string]any, field string, args []any) {
	if len(args) == 0 {
		delete(container, field)
		return
	}
	container[field] = args
}

// scrubStorageNodes replaces every -storageNode flag (in place, same position, with its
// spelling) by a sentinel and returns the endpoints it held per container, args list and
// flag occurrence, so the rest of the object can be compared for equality and each
// endpoint set can be compared with exactly its own counterpart.
func scrubStorageNodes(o map[string]any) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	err := argSites(o, func(site string, container map[string]any, field string, args []any) error {
		tokens, err := tokenize(args, field == "command", "storageNode", dedupName)
		if err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}
		at := map[int]argToken{}
		for _, t := range tokens {
			if t.name == "storageNode" {
				at[t.index] = t
			}
		}
		var rest []any
		occurrence := 0
		for i := 0; i < len(args); i++ {
			t, ok := at[i]
			if !ok {
				rest = append(rest, args[i])
				continue
			}
			nodes := map[string]bool{}
			for _, n := range strings.Split(t.value, ",") {
				n = strings.TrimSpace(n)
				if n == "" || strings.Contains(n, "%{") {
					return fmt.Errorf("%s: -storageNode %q: empty or %%{ENV} endpoint (envflag expands it at start-up); write every endpoint literally", site, t.value)
				}
				nodes[n] = true
			}
			out[fmt.Sprintf("%s#%d", site, occurrence)] = nodes
			occurrence++
			rest = append(rest, storageSentinel+t.spelling())
			if t.consume {
				i++
			}
		}
		setArgs(container, field, rest)
		return nil
	})
	return out, err
}

// scrubDedup removes the exact dedup argument (single dash, inline "=1ms", inside the
// flag zone) from every args list (in place) and returns how many each list held. Any
// other dedup spelling or value stays in the object, so it must then be equal on both
// sides.
func scrubDedup(o map[string]any) (map[string]int, error) {
	out := map[string]int{}
	err := argSites(o, func(site string, container map[string]any, field string, args []any) error {
		tokens, err := tokenize(args, field == "command", "storageNode", dedupName)
		if err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}
		drop := map[int]bool{}
		for _, t := range tokens {
			if s, _ := args[t.index].(string); s == dedupExact {
				drop[t.index] = true
				out[site]++
			}
		}
		var rest []any
		for i, a := range args {
			if !drop[i] {
				rest = append(rest, a)
			}
		}
		setArgs(container, field, rest)
		return nil
	})
	return out, err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// compareRouter allows an insert/select router to differ from its counterpart ONLY by
// endpoints added to a -storageNode flag that already existed at the same place of
// the same container, spelled the same way; every original endpoint must survive in
// its own flag.
func compareRouter(key, fwdFile, origFile string, n, o map[string]any) error {
	newNodes, err := scrubStorageNodes(n)
	if err != nil {
		return fmt.Errorf("%s: router %s %w", fwdFile, key, err)
	}
	oldNodes, err := scrubStorageNodes(o)
	if err != nil {
		return fmt.Errorf("%s: router %s %w", origFile, key, err)
	}
	for _, site := range sortedKeys(oldNodes) {
		have, ok := newNodes[site]
		if !ok {
			return fmt.Errorf("%s: router %s lost its %s flag that %s has", fwdFile, key, site, origFile)
		}
		for _, node := range sortedKeys(oldNodes[site]) {
			if !have[node] {
				return fmt.Errorf("%s: router %s drops original storage endpoint %q from %s", fwdFile, key, node, site)
			}
		}
	}
	for _, site := range sortedKeys(newNodes) {
		if _, ok := oldNodes[site]; !ok {
			return fmt.Errorf("%s: router %s adds %s, which %s does not have; only endpoints of an existing -storageNode flag may be added", fwdFile, key, site, origFile)
		}
	}
	if !reflect.DeepEqual(n, o) {
		return fmt.Errorf("%s: router %s changes more than its -storageNode endpoints compared with %s (or the flag spelling); first difference %v", fwdFile, key, origFile, firstDiff(n, o))
	}
	return nil
}

// firstDiff names the first differing path in either direction, for error messages.
func firstDiff(a, b map[string]any) string {
	if err := within(a, b, "", nil); err != nil {
		return err.Error()
	}
	if err := within(b, a, "", nil); err != nil {
		return "(other side) " + err.Error()
	}
	return "types differ"
}

// compareStorage allows a declared vmstorage workload to differ from its counterpart
// ONLY by one exact -dedup.minScrapeInterval=1ms argument added (concept 10.6, copies
// added). Removal, repetition, any other value or spelling, or any other field is refused.
func compareStorage(key, fwdFile, origFile string, n, o map[string]any) error {
	newDedup, err := scrubDedup(n)
	if err != nil {
		return fmt.Errorf("%s: %s %w", fwdFile, key, err)
	}
	oldDedup, err := scrubDedup(o)
	if err != nil {
		return fmt.Errorf("%s: %s %w", origFile, key, err)
	}
	sites := map[string]bool{}
	for s := range newDedup {
		sites[s] = true
	}
	for s := range oldDedup {
		sites[s] = true
	}
	for _, site := range sortedKeys(sites) {
		if newDedup[site] > 1 || newDedup[site] < oldDedup[site] {
			return fmt.Errorf("%s: %s %s: only a single %s may be added (found %d, %s has %d); removing or repeating it is refused", fwdFile, key, site, dedupExact, newDedup[site], origFile, oldDedup[site])
		}
	}
	if !reflect.DeepEqual(n, o) {
		return fmt.Errorf("%s: %s changes more than the %s argument compared with %s; first difference %v", fwdFile, key, dedupExact, origFile, firstDiff(n, o))
	}
	return nil
}

// routingObjects parses one routing manifest into a map by canonical key.
func routingObjects(file string, c Config) (map[string]object, error) {
	objs, err := parseManifest(file, c.Namespace)
	if err != nil {
		return nil, err
	}
	out := map[string]object{}
	for _, o := range objs {
		out[o.key()] = o
	}
	return out, nil
}

// deepCopy copies a decoded YAML/JSON tree so scrubbing never edits the original.
func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, c := range x {
			m[k] = deepCopy(c)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, c := range x {
			l[i] = deepCopy(c)
		}
		return l
	}
	return v
}

// validateRoutingManifests is the static allow-list for the two storage routing
// manifests (forward and original). Allowed objects: exactly the declared insert/select
// routers and the declared storage_workloads (VictoriaMetrics vmstorage). Both files must
// carry the same objects, so the rollback applies nothing the forward move was not
// checked against, and the only differences are the ones concept 10.6 documents (see
// compareRouter, compareStorage). Alloy, app workloads, Services and anything else are
// refused before any cluster call. liveRouting then checks the files against the live
// objects. It returns both files' objects by key (unscrubbed).
func validateRoutingManifests(c Config) (fwd, orig map[string]object, err error) {
	// The stable Service/endpoint/Alloy-config refusal comes first (clearest message).
	for _, file := range []string{c.RoutingManifest, c.OriginalRoutingManifest} {
		if err = validateProtectedManifest(file, c); err != nil {
			return nil, nil, err
		}
	}
	routers, storage := map[string]bool{}, map[string]bool{}
	for _, r := range c.Routers {
		routers[canonicalResource(r)] = true
	}
	for _, r := range c.Storage {
		storage[canonicalResource(r)] = true
	}
	if fwd, err = routingObjects(c.RoutingManifest, c); err != nil {
		return nil, nil, err
	}
	if orig, err = routingObjects(c.OriginalRoutingManifest, c); err != nil {
		return nil, nil, err
	}
	for _, side := range []struct {
		file string
		objs map[string]object
	}{{c.RoutingManifest, fwd}, {c.OriginalRoutingManifest, orig}} {
		for _, key := range sortedKeys(side.objs) {
			if !routers[key] && !storage[key] {
				return nil, nil, fmt.Errorf("%s: %s is not a declared router or storage workload (routers: %s; storage_workloads: %s); routing manifests may only change those", side.file, key, strings.Join(c.Routers, ", "), strings.Join(c.Storage, ", "))
			}
			if k := side.objs[key].Kind; k != "Deployment" && k != "StatefulSet" {
				return nil, nil, fmt.Errorf("%s: %s must be a Deployment or StatefulSet", side.file, key)
			}
		}
	}
	for _, key := range sortedKeys(fwd) {
		if _, ok := orig[key]; !ok {
			return nil, nil, fmt.Errorf("%s: %s has no counterpart in %s; the rollback could not restore it", c.RoutingManifest, key, c.OriginalRoutingManifest)
		}
	}
	for _, key := range sortedKeys(orig) {
		if _, ok := fwd[key]; !ok {
			return nil, nil, fmt.Errorf("%s: %s has no counterpart in %s; the rollback would apply an object the forward move was never checked against", c.OriginalRoutingManifest, key, c.RoutingManifest)
		}
	}
	for _, key := range sortedKeys(fwd) {
		n, _ := deepCopy(fwd[key].Raw).(map[string]any)
		o, _ := deepCopy(orig[key].Raw).(map[string]any)
		if storage[key] {
			err = compareStorage(key, c.RoutingManifest, c.OriginalRoutingManifest, n, o)
		} else {
			err = compareRouter(key, c.RoutingManifest, c.OriginalRoutingManifest, n, o)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return fwd, orig, nil
}

// singleAtZero writes a copy of single_manifest in which the single is explicitly at
// replicas 0 (an absent replicas field would default to 1), so applying it starts no
// pod: the template roles are patched first, then the single is scaled up. The file
// on disk is never modified. The caller removes the returned path.
func singleAtZero(file, single string) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs []map[string]any
	found := false
	var visit func(m map[string]any) error
	visit = func(m map[string]any) error {
		kind, _ := m["kind"].(string)
		if canonicalKind(kind) == "list" {
			items, _ := m["items"].([]any)
			for _, item := range items {
				if im, ok := item.(map[string]any); ok {
					if err := visit(im); err != nil {
						return err
					}
				}
			}
			return nil
		}
		meta, _ := m["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if canonicalKind(kind)+"/"+name != single {
			return nil
		}
		if k := canonicalKind(kind); k != "statefulset" && k != "deployment" {
			return fmt.Errorf("%s: the single %s must be a statefulset or deployment to restore it at replicas 0", file, single)
		}
		spec, _ := m["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
			m["spec"] = spec
		}
		spec["replicas"] = 0
		found = true
		return nil
	}
	for {
		var m map[string]any
		if err = dec.Decode(&m); err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", file, err)
		}
		if m == nil {
			continue
		}
		if err = visit(m); err != nil {
			return "", err
		}
		docs = append(docs, m)
	}
	if !found {
		return "", fmt.Errorf("%s: does not contain the single %s", file, single)
	}
	tmp, err := os.CreateTemp("", "obs-single-at-zero-*.yaml")
	if err != nil {
		return "", err
	}
	enc := yaml.NewEncoder(tmp)
	for _, d := range docs {
		if err = enc.Encode(d); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", err
		}
	}
	if err = enc.Close(); err == nil {
		err = tmp.Close()
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
