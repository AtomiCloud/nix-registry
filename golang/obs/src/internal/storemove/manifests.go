package storemove

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"reflect"
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
	for i := range c.Scaled {
		c.Scaled[i] = canonicalResource(c.Scaled[i])
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

// stripStorageNodes removes every -storageNode flag from every container of a
// workload (in place) and returns the endpoints it held, so the rest of the object
// can be compared for equality.
func stripStorageNodes(o map[string]any) map[string]bool {
	nodes := map[string]bool{}
	spec, _ := o["spec"].(map[string]any)
	tpl, _ := spec["template"].(map[string]any)
	pod, _ := tpl["spec"].(map[string]any)
	for _, group := range []string{"containers", "initContainers"} {
		containers, _ := pod[group].([]any)
		for _, c := range containers {
			container, _ := c.(map[string]any)
			for _, field := range []string{"args", "command"} {
				args, ok := container[field].([]any)
				if !ok {
					continue
				}
				var rest []any
				for i := 0; i < len(args); i++ {
					s, isString := args[i].(string)
					flag := strings.TrimLeft(s, "-")
					switch {
					case isString && flag == "storageNode" && i+1 < len(args):
						i++
						s, _ = args[i].(string)
						for _, n := range strings.Split(s, ",") {
							nodes[strings.TrimSpace(n)] = true
						}
					case isString && (flag == "dedup.minScrapeInterval" || strings.HasPrefix(flag, "dedup.minScrapeInterval=")):
						// The one other documented routing change (VM copies); its value is free.
						if flag == "dedup.minScrapeInterval" && i+1 < len(args) {
							i++
						}
					case isString && strings.HasPrefix(flag, "storageNode="):
						for _, n := range strings.Split(strings.TrimPrefix(flag, "storageNode="), ",") {
							nodes[strings.TrimSpace(n)] = true
						}
					default:
						rest = append(rest, args[i])
					}
				}
				if len(rest) == 0 {
					delete(container, field)
				} else {
					container[field] = rest
				}
			}
		}
	}
	return nodes
}

// validateRoutingManifests lets the two storage routing manifests (add endpoints and
// the original) mutate ONLY the declared insert/select routers, and within those only
// the -storageNode list; every original endpoint must survive in the new list. Alloy,
// app workloads, Services and anything else are refused before any apply.
func validateRoutingManifests(c Config) error {
	// The stable Service/endpoint/Alloy-config refusal comes first (clearest message).
	for _, file := range []string{c.RoutingManifest, c.OriginalRoutingManifest} {
		if err := validateProtectedManifest(file, c); err != nil {
			return err
		}
	}
	declared := map[string]bool{}
	for _, r := range c.Routers {
		declared[canonicalResource(r)] = true
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
		for key := range objs {
			if !declared[key] {
				return fmt.Errorf("%s: %s is not a declared router (%s); routing manifests may only change the insert/select routers' -storageNode list", file, key, strings.Join(c.Routers, ", "))
			}
		}
	}
	for key, n := range newObjs {
		o, ok := oldObjs[key]
		if !ok {
			return fmt.Errorf("%s: router %s has no original in %s; cannot show that only -storageNode changes", c.RoutingManifest, key, c.OriginalRoutingManifest)
		}
		newNodes, oldNodes := stripStorageNodes(n), stripStorageNodes(o)
		for node := range oldNodes {
			if !newNodes[node] {
				return fmt.Errorf("%s: router %s drops original storage endpoint %q", c.RoutingManifest, key, node)
			}
		}
		if !reflect.DeepEqual(n, o) {
			return fmt.Errorf("%s: router %s changes more than its -storageNode list compared with %s", c.RoutingManifest, key, c.OriginalRoutingManifest)
		}
	}
	return nil
}
