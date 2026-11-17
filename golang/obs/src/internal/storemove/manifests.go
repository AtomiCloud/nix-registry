package storemove

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
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

type manifestObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name            string            `yaml:"name"`
		Labels          map[string]string `yaml:"labels"`
		OwnerReferences []struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"ownerReferences"`
	} `yaml:"metadata"`
	Spec struct {
		Replicas *int `yaml:"replicas"`
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
		} `yaml:"template"`
	} `yaml:"spec"`
	Items []manifestObject `yaml:"items"`
}

func manifestObjects(file string) ([]manifestObject, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var out []manifestObject
	var add func(manifestObject)
	add = func(o manifestObject) {
		if canonicalKind(o.Kind) == "list" {
			for _, item := range o.Items {
				add(item)
			}
		} else if o.Kind != "" {
			out = append(out, o)
		}
	}
	for {
		var obj manifestObject
		if err = dec.Decode(&obj); err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		add(obj)
	}
}
func stableService(name string) bool {
	switch name {
	case "vl-insert", "vl-select", "tempo-distributor", "tempo-query", "vm-insert", "vm-select", "pyroscope-write", "pyroscope-query":
		return true
	}
	return false
}
func protectedObject(o manifestObject, c Config) bool {
	kind := canonicalKind(o.Kind)
	if kind == "service" && stableService(o.Metadata.Name) {
		return true
	}
	if kind == "endpoints" || kind == "endpointslice" {
		if stableService(o.Metadata.Name) || stableService(o.Metadata.Labels["kubernetes.io/service-name"]) {
			return true
		}
		for _, owner := range o.Metadata.OwnerReferences {
			if canonicalKind(owner.Kind) == "service" && stableService(owner.Name) {
				return true
			}
		}
	}
	if kind == "configmap" || kind == "secret" {
		_, alloyName, _ := strings.Cut(c.Alloy, "/")
		if o.Metadata.Name == "alloy" || strings.HasPrefix(o.Metadata.Name, "alloy-") || alloyName != "" && o.Metadata.Name == alloyName || o.Metadata.Labels["app.kubernetes.io/name"] == "alloy" {
			return true
		}
		for _, r := range c.AlloyConfigObjects {
			if canonicalResource(r) == kind+"/"+o.Metadata.Name {
				return true
			}
		}
	}
	return false
}
func validateProtectedManifest(file string, c Config) error {
	objs, err := manifestObjects(file)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if protectedObject(o, c) {
			return fmt.Errorf("%s: protected stable Service/endpoint or Alloy config %s/%s must not change", file, o.Kind, o.Metadata.Name)
		}
	}
	return nil
}
func validateIsolatedManifest(file string, c Config) error {
	objs, err := manifestObjects(file)
	if err != nil {
		return err
	}
	for _, o := range objs {
		for _, role := range c.RoleLabels {
			if o.Spec.Template.Metadata.Labels[role] == "true" || o.Metadata.Labels[role] == "true" {
				return fmt.Errorf("%s: %s/%s role %s=true routes before the role step", file, o.Kind, o.Metadata.Name, role)
			}
		}
	}
	return nil
}
func validateScaledManifest(c Config) error {
	c = c.normalized()
	objs, err := manifestObjects(c.ScaledManifest)
	if err != nil {
		return err
	}
	if err = validateProtectedManifest(c.ScaledManifest, c); err != nil {
		return err
	}
	if err = validateKinds(c.ScaledManifest, objs); err != nil {
		return err
	}
	if err = validateIsolatedManifest(c.ScaledManifest, c); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, o := range objs {
		key := canonicalKind(o.Kind) + "/" + o.Metadata.Name
		if key == c.Single || key == c.Alloy {
			return fmt.Errorf("%s: scaled_manifest must not change old single or Alloy %s before routing and drain", c.ScaledManifest, key)
		}
		for _, w := range c.Scaled {
			if key == w {
				if o.Spec.Replicas == nil || *o.Spec.Replicas < 1 {
					return fmt.Errorf("%s: %s requires positive explicit replicas", c.ScaledManifest, key)
				}
				seen[key] = true
			}
		}
		switch canonicalKind(o.Kind) {
		case "deployment", "statefulset", "daemonset", "job", "pod":
			if !seen[key] {
				return fmt.Errorf("%s: workload %s is not listed in scaled; refusing to skip its readiness", c.ScaledManifest, key)
			}
		}
	}
	for _, w := range c.Scaled {
		if !seen[w] {
			return fmt.Errorf("%s: scaled workload %s is missing", c.ScaledManifest, w)
		}
	}
	return nil
}

// rawObjects decodes a manifest without schema loss (Lists flattened) so a routing
// change can be compared field by field.
func rawObjects(file string) (map[string]map[string]any, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	out := map[string]map[string]any{}
	var add func(map[string]any) error
	add = func(o map[string]any) error {
		kind, _ := o["kind"].(string)
		if canonicalKind(kind) == "list" {
			items, _ := o["items"].([]any)
			for _, item := range items {
				if m, ok := item.(map[string]any); ok {
					if err := add(m); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if kind == "" {
			return nil
		}
		meta, _ := o["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		key := canonicalKind(kind) + "/" + name
		if _, dup := out[key]; dup {
			return fmt.Errorf("%s: %s appears twice", file, key)
		}
		out[key] = o
		return nil
	}
	for {
		var obj map[string]any
		if err = dec.Decode(&obj); err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if obj != nil {
			if err = add(obj); err != nil {
				return nil, err
			}
		}
	}
}

const (
	// dedupExact is the one -dedup.minScrapeInterval setting concept 10.6 documents
	// (vmstorage x2, "=1ms"); no other value or form is ever accepted.
	dedupExact = "-dedup.minScrapeInterval=1ms"
	// storageSentinel stands in for a -storageNode flag and its value, at the same
	// position, once its endpoints have been read out.
	storageSentinel = "\x00-storageNode"
)

// argSites calls fn for every args/command list of every container and init
// container of a workload, with a stable site name such as "containers[0](c).args".
func argSites(o map[string]any, fn func(site string, container map[string]any, field string, args []any)) {
	spec, _ := o["spec"].(map[string]any)
	tpl, _ := spec["template"].(map[string]any)
	pod, _ := tpl["spec"].(map[string]any)
	for _, group := range []string{"initContainers", "containers"} {
		containers, _ := pod[group].([]any)
		for i, c := range containers {
			container, _ := c.(map[string]any)
			name, _ := container["name"].(string)
			for _, field := range []string{"command", "args"} {
				if args, ok := container[field].([]any); ok {
					fn(fmt.Sprintf("%s[%d](%s).%s", group, i, name, field), container, field, args)
				}
			}
		}
	}
}

func setArgs(container map[string]any, field string, args []any) {
	if len(args) == 0 {
		delete(container, field)
		return
	}
	container[field] = args
}

// scrubStorageNodes replaces every -storageNode flag (in place, same position) by a
// sentinel and returns the endpoints it held per container, args list and flag
// occurrence, so the rest of the object can be compared for equality and each
// endpoint set can be compared with exactly its own counterpart.
func scrubStorageNodes(o map[string]any) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	argSites(o, func(site string, container map[string]any, field string, args []any) {
		var rest []any
		occurrence := 0
		take := func(list string) {
			nodes := map[string]bool{}
			for _, n := range strings.Split(list, ",") {
				nodes[strings.TrimSpace(n)] = true
			}
			out[fmt.Sprintf("%s#%d", site, occurrence)] = nodes
			occurrence++
			rest = append(rest, storageSentinel)
		}
		for i := 0; i < len(args); i++ {
			s, isString := args[i].(string)
			flag := strings.TrimLeft(s, "-")
			switch {
			case isString && flag == "storageNode" && i+1 < len(args):
				i++
				next, _ := args[i].(string)
				take(next)
			case isString && strings.HasPrefix(flag, "storageNode="):
				take(strings.TrimPrefix(flag, "storageNode="))
			default:
				rest = append(rest, args[i])
			}
		}
		setArgs(container, field, rest)
	})
	return out
}

// scrubDedup removes the exact dedup argument from every args list (in place) and
// returns how many each list held. Any other dedup flag or value stays in the
// object, so it must then be byte-for-byte equal on both sides.
func scrubDedup(o map[string]any) map[string]int {
	out := map[string]int{}
	argSites(o, func(site string, container map[string]any, field string, args []any) {
		var rest []any
		for _, a := range args {
			if s, ok := a.(string); ok && s == dedupExact {
				out[site]++
				continue
			}
			rest = append(rest, a)
		}
		setArgs(container, field, rest)
	})
	return out
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
// the same container; every original endpoint must survive in its own flag.
func compareRouter(key, fwdFile, origFile string, n, o map[string]any) error {
	newNodes, oldNodes := scrubStorageNodes(n), scrubStorageNodes(o)
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
		return fmt.Errorf("%s: router %s changes more than its -storageNode endpoints compared with %s", fwdFile, key, origFile)
	}
	return nil
}

// compareStorage allows a declared vmstorage workload to differ from its counterpart
// ONLY by one exact -dedup.minScrapeInterval=1ms argument added (concept 10.6, copies
// added). Removal, any other value or any other field is refused.
func compareStorage(key, fwdFile, origFile string, n, o map[string]any) error {
	newDedup, oldDedup := scrubDedup(n), scrubDedup(o)
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
		return fmt.Errorf("%s: %s changes more than the %s argument compared with %s", fwdFile, key, dedupExact, origFile)
	}
	return nil
}

// validateRoutingManifests is the allow-list for the two storage routing manifests
// (forward and original). Allowed objects: exactly the declared insert/select
// routers and the declared storage_workloads (VictoriaMetrics vmstorage). Both
// files must carry the same objects, so the rollback applies nothing the forward
// move was not checked against, and the only differences are the ones concept 10.6
// documents (see compareRouter, compareStorage). Alloy, app workloads, Services and
// anything else are refused before any apply.
func validateRoutingManifests(c Config) error {
	// The stable Service/endpoint/Alloy-config refusal comes first (clearest message).
	for _, file := range []string{c.RoutingManifest, c.OriginalRoutingManifest} {
		if err := validateProtectedManifest(file, c); err != nil {
			return err
		}
	}
	routers, storage := map[string]bool{}, map[string]bool{}
	for _, r := range c.Routers {
		routers[canonicalResource(r)] = true
	}
	for _, r := range c.Storage {
		storage[canonicalResource(r)] = true
	}
	newObjs, err := rawObjects(c.RoutingManifest)
	if err != nil {
		return err
	}
	oldObjs, err := rawObjects(c.OriginalRoutingManifest)
	if err != nil {
		return err
	}
	for file, objs := range map[string]map[string]map[string]any{c.RoutingManifest: newObjs, c.OriginalRoutingManifest: oldObjs} {
		for _, key := range sortedKeys(objs) {
			if !routers[key] && !storage[key] {
				return fmt.Errorf("%s: %s is not a declared router or storage workload (routers: %s; storage_workloads: %s); routing manifests may only change those", file, key, strings.Join(c.Routers, ", "), strings.Join(c.Storage, ", "))
			}
		}
	}
	for _, key := range sortedKeys(newObjs) {
		if _, ok := oldObjs[key]; !ok {
			return fmt.Errorf("%s: %s has no counterpart in %s; the rollback could not restore it", c.RoutingManifest, key, c.OriginalRoutingManifest)
		}
	}
	for _, key := range sortedKeys(oldObjs) {
		if _, ok := newObjs[key]; !ok {
			return fmt.Errorf("%s: %s has no counterpart in %s; the rollback would apply an object the forward move was never checked against", c.OriginalRoutingManifest, key, c.RoutingManifest)
		}
	}
	for _, key := range sortedKeys(newObjs) {
		if storage[key] {
			err = compareStorage(key, c.RoutingManifest, c.OriginalRoutingManifest, newObjs[key], oldObjs[key])
		} else {
			err = compareRouter(key, c.RoutingManifest, c.OriginalRoutingManifest, newObjs[key], oldObjs[key])
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Kinds the three non-routing manifests may carry. Anything else (ClusterRole,
// Namespace, CronJob, Ingress, NetworkPolicy, ...) is refused: the move changes
// storage workloads and their own config/storage/Service objects, nothing else.
var allowedKinds = map[string]bool{
	"statefulset": true, "deployment": true, "daemonset": true, "service": true, "configmap": true, "secret": true,
	"persistentvolumeclaim": true, "serviceaccount": true, "role": true, "rolebinding": true,
}

func workloadKind(kind string) bool {
	switch canonicalKind(kind) {
	case "deployment", "statefulset", "daemonset", "job", "cronjob", "pod", "replicaset":
		return true
	}
	return false
}

func validateKinds(file string, objs []manifestObject) error {
	for _, o := range objs {
		if !allowedKinds[canonicalKind(o.Kind)] {
			return fmt.Errorf("%s: %s/%s is not an allowed kind (allowed: statefulset, deployment, daemonset, service, configmap, secret, persistentvolumeclaim, serviceaccount, role, rolebinding)", file, o.Kind, o.Metadata.Name)
		}
	}
	return nil
}

// validateSingleManifest covers single_manifest (rollback restore) and
// metastore_manifest (Pyroscope retain): the original single is the ONLY workload,
// next to its own non-protected Service/config/storage objects.
func validateSingleManifest(file string, c Config) error {
	objs, err := manifestObjects(file)
	if err != nil {
		return err
	}
	if err = validateProtectedManifest(file, c); err != nil {
		return err
	}
	if err = validateKinds(file, objs); err != nil {
		return err
	}
	found := false
	for _, o := range objs {
		key := canonicalKind(o.Kind) + "/" + o.Metadata.Name
		if workloadKind(o.Kind) && key != c.Single {
			return fmt.Errorf("%s: workload %s is not the single %s; refusing to apply it", file, key, c.Single)
		}
		if key == c.Single {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%s: does not contain the single %s", file, c.Single)
	}
	return nil
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
