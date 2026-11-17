package storemove

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Every scaled workload must be present and isolated before step 2. Services are
// deliberately excluded: their selectors and ports must remain stable.
func validateScaledManifest(c Config) error {
	b, err := os.ReadFile(c.ScaledManifest)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	seen := map[string]bool{}
	for {
		var obj struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Replicas *int `yaml:"replicas"`
				Template struct {
					Metadata struct {
						Labels map[string]string `yaml:"labels"`
					} `yaml:"metadata"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err = dec.Decode(&obj); err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", c.ScaledManifest, err)
		}
		if obj.Kind == "" {
			continue
		}
		if obj.Kind == "Service" || obj.Kind == "List" {
			return fmt.Errorf("%s: %s is forbidden in scaled_manifest; stable Services do not change", c.ScaledManifest, obj.Kind)
		}
		key := strings.ToLower(obj.Kind) + "/" + obj.Metadata.Name
		if key == c.Single || key == c.Alloy {
			return fmt.Errorf("%s: scaled_manifest must not change old single or Alloy %s before routing and drain", c.ScaledManifest, key)
		}
		for _, role := range c.RoleLabels {
			if obj.Spec.Template.Metadata.Labels[role] == "true" {
				return fmt.Errorf("%s: %s role %s=true routes traffic before Ready; start isolated", c.ScaledManifest, key, role)
			}
		}
		for _, w := range c.Scaled {
			if key == w {
				if obj.Spec.Replicas == nil || *obj.Spec.Replicas < 1 {
					return fmt.Errorf("%s: %s requires positive explicit replicas", c.ScaledManifest, key)
				}
				seen[key] = true
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
