package storemove

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// The scaled manifest holds only NEW objects (the scaled workloads or new storage nodes
// and their supporting objects). obs creates them with `kubectl create` (never apply):
// the API server refuses any object that already exists, so the manifest can never
// change an existing object. Before creating, every object, every claim its
// volumeClaimTemplates would bind and every pod its selector would adopt must be absent
// live. obs writes the objects it validated to its own file, adding to each the owner
// label and a digest of the validated object, so a re-run recognises exactly the
// objects this manifest created and nothing else.

// ownerLabel marks an object obs store move created; digestAnnotation binds it to the
// exact manifest object it was created from.
const (
	ownerLabel       = "obs.diene/store-move"
	digestAnnotation = "obs.diene/store-move-digest"
)

func stableService(name string) bool {
	switch name {
	case "vl-insert", "vl-select", "tempo-distributor", "tempo-query", "vm-insert", "vm-select", "pyroscope-write", "pyroscope-query":
		return true
	}
	return false
}

// stableServices are the concept 10.2 Services whose selectors the role flip relies on.
func stableServices(store string) []string {
	switch store {
	case "tempo":
		return []string{"tempo-distributor", "tempo-query"}
	case "pyroscope":
		return []string{"pyroscope-write", "pyroscope-query"}
	}
	return nil
}

func alloyConfig(o object, c Config) bool {
	kind := canonicalKind(o.Kind)
	if kind != "configmap" {
		return false
	}
	labels := stringMap(mapAt(o.Raw, "metadata")["labels"])
	_, alloyName, _ := strings.Cut(c.Alloy, "/")
	if o.Name == "alloy" || strings.HasPrefix(o.Name, "alloy-") || alloyName != "" && o.Name == alloyName || labels["app.kubernetes.io/name"] == "alloy" {
		return true
	}
	for _, r := range c.AlloyConfigObjects {
		if canonicalResource(r) == kind+"/"+o.Name {
			return true
		}
	}
	return false
}

// existingRefs are the objects the move changes in place (by obs-built patches only);
// none of them may appear in the scaled manifest.
func existingRefs(c Config) map[string]bool {
	out := map[string]bool{}
	for _, r := range append([]string{c.Single, c.Alloy, c.Compactor}, c.Storage...) {
		if r != "" {
			out[r] = true
		}
	}
	for _, r := range c.Routers {
		out[r.Workload] = true
	}
	return out
}

// ownNames are the names a supporting object of the scaled form may carry: a scaled
// workload or the single, alone or as a "<name>-" prefix (tempo-live-store-headless,
// pyroscope-metastore). Everything else, such as an app's checkout-env, is not the store's.
func ownNames(c Config) []string {
	var names []string
	for _, r := range append(append([]string(nil), c.Scaled...), c.Single) {
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
func manifestHas(objs []object, kind, name string) bool {
	for _, o := range objs {
		if o.Kind == kind && o.Name == name {
			return true
		}
	}
	return false
}

// validateSupporting is the per-kind allow-list for the non-workload objects (concept
// 10.5): headless Services (Tempo: publishNotReadyAddresses true), ConfigMaps, and for
// Pyroscope only the ServiceAccount, Role and RoleBinding that kubernetes:/// metastore
// discovery needs (read pods/endpoints only).
func validateSupporting(file string, o object, objs []object, store string, c Config) error {
	if !ownName(o.Name, c) {
		return fmt.Errorf("%s: %s/%s is not one of the store's own names (a scaled workload or the single, or <name>-...: %s)", file, o.Kind, o.Name, strings.Join(ownNames(c), ", "))
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

// validatePod refuses pod-template settings a new store pod never needs and that would
// reach beyond its own objects: another claim or a host path (new storage comes only
// from volumeClaimTemplates), the host's network/PID/IPC namespaces, and a ServiceAccount
// the manifest does not create (running as an existing account would borrow its rights).
func validatePod(file string, o object, objs []object) error {
	pod := mapAt(o.Raw, "spec", "template", "spec")
	vols, _ := pod["volumes"].([]any)
	for i, v := range vols {
		vm, _ := v.(map[string]any)
		for _, k := range []string{"persistentVolumeClaim", "hostPath"} {
			if _, ok := vm[k]; ok {
				return fmt.Errorf("%s: %s/%s volumes[%d] uses %s; new workloads get storage only from volumeClaimTemplates", file, o.Kind, o.Name, i, k)
			}
		}
	}
	for _, k := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if pod[k] == true {
			return fmt.Errorf("%s: %s/%s sets %s; a store pod never needs the host's namespaces", file, o.Kind, o.Name, k)
		}
	}
	for _, k := range []string{"serviceAccountName", "serviceAccount"} {
		sa, _ := pod[k].(string)
		if sa != "" && sa != "default" && !manifestHas(objs, "ServiceAccount", sa) {
			return fmt.Errorf("%s: %s/%s %s %q is not created by this manifest; new pods may only run as their own ServiceAccount", file, o.Kind, o.Name, k, sa)
		}
	}
	return nil
}

// selectorOf reads a workload's matchLabels selector as "k=v,..." (sorted); expressions
// or an empty selector are refused, and role labels never occur in it (they change).
func selectorOf(m map[string]any, what string, roles []string) (map[string]string, error) {
	sel := mapAt(m, "spec", "selector")
	labels := stringMap(sel["matchLabels"])
	if len(labels) == 0 || sel["matchExpressions"] != nil {
		return nil, fmt.Errorf("%s: require a nonempty matchLabels selector without expressions", what)
	}
	for _, role := range roles {
		if _, ok := labels[role]; ok {
			return nil, fmt.Errorf("%s: role %s must not occur in the immutable workload selector", what, role)
		}
	}
	return labels, nil
}
func selectorString(labels map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(labels) {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ",")
}

// disjoint reports whether no pod can match both selectors (some key differs).
func disjoint(a, b map[string]string) bool {
	for k, v := range a {
		if w, ok := b[k]; ok && w != v {
			return true
		}
	}
	return false
}

func intField(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
	}
	return 0, false
}

// validateScaledManifest is the static allow-list of scaled_manifest (no cluster read).
func validateScaledManifest(c Config, store string) ([]object, error) {
	c = c.normalized()
	file := c.ScaledManifest
	objs, err := parseManifest(file, c.Namespace)
	if err != nil {
		return nil, err
	}
	scaled := map[string]bool{}
	for _, w := range c.Scaled {
		scaled[w] = true
	}
	existing := existingRefs(c)
	seen := map[string]bool{}
	selectors := map[string]map[string]string{}
	for _, o := range objs {
		key := o.key()
		meta := mapAt(o.Raw, "metadata")
		if canonicalKind(o.Kind) == "service" && stableService(o.Name) || alloyConfig(o, c) {
			return nil, fmt.Errorf("%s: protected stable Service or Alloy config %s/%s must not change", file, o.Kind, o.Name)
		}
		if existing[key] {
			return nil, fmt.Errorf("%s: %s is an existing object the move changes itself; the scaled manifest holds only new objects", file, key)
		}
		if own, ok := stringMap(meta["labels"])[ownerLabel]; ok && own != store {
			return nil, fmt.Errorf("%s: %s label %s=%s, want %s (obs sets it)", file, key, ownerLabel, own, store)
		}
		for _, a := range []string{digestAnnotation, inverseAnnotation} {
			if _, ok := mapAt(meta, "annotations")[a]; ok {
				return nil, fmt.Errorf("%s: %s sets annotation %s, which only obs writes", file, key, a)
			}
		}
		tpl := stringMap(mapAt(o.Raw, "spec", "template", "metadata")["labels"])
		own := stringMap(meta["labels"])
		for _, role := range c.RoleLabels {
			if tpl[role] == "true" || own[role] == "true" {
				return nil, fmt.Errorf("%s: %s role %s=true routes before the role step", file, key, role)
			}
		}
		switch o.Kind {
		case "StatefulSet", "Deployment":
			if !scaled[key] {
				return nil, fmt.Errorf("%s: workload %s is not listed in scaled; refusing to skip its readiness", file, key)
			}
			if storage(store) && o.Kind != "StatefulSet" {
				return nil, fmt.Errorf("%s: %s: new storage nodes are StatefulSets (stable per-pod names)", file, key)
			}
			if r, ok := intField(mapAt(o.Raw, "spec")["replicas"]); !ok || r < 1 {
				return nil, fmt.Errorf("%s: %s requires positive explicit replicas", file, key)
			}
			if err = validatePod(file, o, objs); err != nil {
				return nil, err
			}
			sel, err := selectorOf(o.Raw, file+": "+key, c.RoleLabels)
			if err != nil {
				return nil, err
			}
			for other, s := range selectors {
				if !disjoint(sel, s) {
					return nil, fmt.Errorf("%s: %s and %s pod selectors may overlap; require distinct component labels", file, key, other)
				}
			}
			selectors[key] = sel
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
	if storage(store) {
		if _, err := storageNodes(c, objs); err != nil {
			return nil, err
		}
	}
	return objs, nil
}

// claimNames are the PersistentVolumeClaims a StatefulSet binds: <template>-<name>-<i>
// for every volumeClaimTemplate and replica index (Kubernetes StatefulSet naming). A
// claim that already exists would be adopted, so all must be absent before create.
func claimNames(o object) []string {
	if o.Kind != "StatefulSet" {
		return nil
	}
	n, _ := intField(mapAt(o.Raw, "spec")["replicas"])
	tpls, _ := mapAt(o.Raw, "spec")["volumeClaimTemplates"].([]any)
	var out []string
	for _, t := range tpls {
		tm, _ := t.(map[string]any)
		name, _ := mapAt(tm, "metadata")["name"].(string)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf("%s-%s-%d", name, o.Name, i))
		}
	}
	return out
}

// storageNode is one new VictoriaLogs/VictoriaMetrics storage StatefulSet.
type storageNode struct {
	obj      object
	service  string
	replicas int
}

// storageNodes reads the new storage StatefulSets and checks what the endpoints are
// derived from: spec.serviceName names a headless Service of this manifest that selects
// the pods (so <pod>.<service> resolves), every router port is a containerPort of the
// pods, and for two copies every storage container carries -dedup.minScrapeInterval=1ms.
func storageNodes(c Config, objs []object) ([]storageNode, error) {
	file := c.ScaledManifest
	var out []storageNode
	for _, w := range c.Scaled {
		var o *object
		for i := range objs {
			if objs[i].key() == w {
				o = &objs[i]
			}
		}
		if o == nil || o.Kind != "StatefulSet" {
			return nil, fmt.Errorf("%s: storage node %s must be a StatefulSet in the manifest", file, w)
		}
		spec := mapAt(o.Raw, "spec")
		svc, _ := spec["serviceName"].(string)
		var service *object
		for i := range objs {
			if objs[i].Kind == "Service" && objs[i].Name == svc {
				service = &objs[i]
			}
		}
		if svc == "" || service == nil {
			return nil, fmt.Errorf("%s: %s spec.serviceName %q must name a headless Service in this manifest (the storage endpoints are <pod>.<service>)", file, w, svc)
		}
		sel := stringMap(mapAt(service.Raw, "spec")["selector"])
		tpl := stringMap(mapAt(spec, "template", "metadata")["labels"])
		if len(sel) == 0 {
			return nil, fmt.Errorf("%s: Service/%s needs a selector for %s's pods", file, svc, w)
		}
		for k, v := range sel {
			if tpl[k] != v {
				return nil, fmt.Errorf("%s: Service/%s selector %s=%s does not select %s's pods", file, svc, k, v, w)
			}
		}
		ports := map[int]bool{}
		containers, _ := mapAt(spec, "template", "spec")["containers"].([]any)
		for _, ct := range containers {
			cm, _ := ct.(map[string]any)
			ps, _ := cm["ports"].([]any)
			for _, p := range ps {
				if n, ok := intField(mapAt(map[string]any{"p": p}, "p")["containerPort"]); ok {
					ports[n] = true
				}
			}
		}
		for _, r := range c.Routers {
			if !ports[r.Port] {
				return nil, fmt.Errorf("%s: %s declares no containerPort %d, the port router %s dials", file, w, r.Port, r.Workload)
			}
		}
		if c.Copies == 2 {
			for i, ct := range containers {
				cm, _ := ct.(map[string]any)
				hits, err := findFlags(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{cm}}}}}, dedupName)
				if err != nil {
					return nil, fmt.Errorf("%s: %s containers[%d]: %w", file, w, i, err)
				}
				if len(hits) != 1 || hits[0].tok.value != "1ms" {
					return nil, fmt.Errorf("%s: %s containers[%d]: two copies need exactly one -%s=1ms on every vmstorage (concept 10.5)", file, w, i, dedupName)
				}
			}
		}
		n, _ := intField(spec["replicas"])
		out = append(out, storageNode{*o, svc, n})
	}
	return out, nil
}

// endpoints are the derived -storageNode values for one router port: every pod of every
// new storage StatefulSet by its stable DNS name. Nobody types them.
func endpoints(c Config, nodes []storageNode, port int) []string {
	var out []string
	for _, n := range nodes {
		for i := 0; i < n.replicas; i++ {
			out = append(out, fmt.Sprintf("%s-%d.%s.%s.svc.%s:%d", n.obj.Name, i, n.service, c.Namespace, c.ClusterDomain, port))
		}
	}
	return out
}

// digest identifies one validated manifest object (map keys marshal sorted).
func digest(o object) string {
	b, _ := json.Marshal(norm(o.Raw))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// createFile writes the objects obs creates: exactly the validated objects (Lists
// flattened), in the configured namespace, each with the owner label and its digest.
// The caller removes the file.
func createFile(c Config, store string, objs []object) (string, error) {
	tmp, err := os.CreateTemp("", "obs-store-move-create-*.yaml")
	if err != nil {
		return "", err
	}
	enc := yaml.NewEncoder(tmp)
	for _, o := range objs {
		m, _ := deepCopy(o.Raw).(map[string]any)
		meta, _ := m["metadata"].(map[string]any)
		meta["namespace"] = c.Namespace
		labels, _ := meta["labels"].(map[string]any)
		if labels == nil {
			labels = map[string]any{}
			meta["labels"] = labels
		}
		labels[ownerLabel] = store
		ann, _ := meta["annotations"].(map[string]any)
		if ann == nil {
			ann = map[string]any{}
			meta["annotations"] = ann
		}
		ann[digestAnnotation] = digest(o)
		if err = enc.Encode(m); err != nil {
			break
		}
	}
	if err == nil {
		err = enc.Close()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
