package storemove

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
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
