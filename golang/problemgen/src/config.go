package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config is problemgen.yaml: where the definitions are and what to generate. It is the
// only place that knows platform and service at generation time. The error portal is a
// run-time value (errorPortal.url); the optional portal here is only the record's default.
type Config struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Platform   string         `yaml:"platform"`
	Service    string         `yaml:"service"`
	Portal     string         `yaml:"portal"`
	Modules    []ModuleConfig `yaml:"modules"`
	Dir        string         `yaml:"-"`
}

// ModuleConfig maps one definition file to its outputs.
type ModuleConfig struct {
	Definition string  `yaml:"definition"`
	Outputs    Outputs `yaml:"outputs"`
}

// Outputs lists the generated files for one module. Every output is optional.
type Outputs struct {
	Go         *GoOut   `yaml:"go"`
	TypeScript *TSOut   `yaml:"typescript"`
	CSharp     *CSOut   `yaml:"csharp"`
	Dart       *DartOut `yaml:"dart"`
	Record     *FileOut `yaml:"record"`
	Catalog    *FileOut `yaml:"catalog"`
}

// GoOut configures the Go output.
type GoOut struct {
	File    string `yaml:"file"`
	Package string `yaml:"package"`
	Runtime string `yaml:"runtime"` // import path of the problems runtime package
}

// TSOut configures the TypeScript output.
type TSOut struct {
	File    string `yaml:"file"`
	Runtime string `yaml:"runtime"` // module specifier of the problems runtime
}

// CSOut configures the C# output.
type CSOut struct {
	File      string `yaml:"file"`
	Namespace string `yaml:"namespace"`
	Runtime   string `yaml:"runtime"` // namespace of the problems runtime
}

// DartOut configures the Dart output.
type DartOut struct {
	File    string `yaml:"file"`
	Runtime string `yaml:"runtime"` // import URI of the problems runtime library
}

// FileOut is an output that is only a path.
type FileOut struct {
	File string `yaml:"file"`
}

var (
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`) // RFC 1123 label, at most 63
	portalRe = regexp.MustCompile(portalOriginRe)
)

// LoadConfig reads and checks problemgen.yaml.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Dir = filepath.Dir(path)
	if c.APIVersion != "problems.atomi.cloud/v1" || c.Kind != "GeneratorConfig" {
		return nil, fmt.Errorf("%s: want apiVersion problems.atomi.cloud/v1, kind GeneratorConfig", path)
	}
	if !dnsLabel.MatchString(c.Platform) || !dnsLabel.MatchString(c.Service) {
		return nil, fmt.Errorf("%s: platform and service must be DNS labels (a-z, 0-9, -; at most 63 characters)", path)
	}
	if c.Portal != "" && !validPortal(c.Portal) {
		return nil, fmt.Errorf("%s: portal (optional) %s", path, portalMsg)
	}
	if len(c.Modules) == 0 {
		return nil, fmt.Errorf("%s: modules is empty: list every definition (an empty list would verify nothing)", path)
	}
	seen := map[string]bool{}
	for _, mc := range c.Modules {
		if mc.Definition == "" {
			return nil, fmt.Errorf("%s: a module has no definition", path)
		}
		o := mc.Outputs
		if o.Go == nil && o.TypeScript == nil && o.CSharp == nil && o.Dart == nil && o.Record == nil && o.Catalog == nil {
			return nil, fmt.Errorf("%s: module %s has no outputs (it would verify nothing)", path, mc.Definition)
		}
		// An enabled output with no file is skipped by planOutputs (or, for record and catalog,
		// aims at the config directory): generate would write nothing and verify would pass.
		for _, e := range []struct {
			name string
			set  bool
			file func() string
		}{
			{"go", o.Go != nil, func() string { return o.Go.File }},
			{"typescript", o.TypeScript != nil, func() string { return o.TypeScript.File }},
			{"csharp", o.CSharp != nil, func() string { return o.CSharp.File }},
			{"dart", o.Dart != nil, func() string { return o.Dart.File }},
			{"record", o.Record != nil, func() string { return o.Record.File }},
			{"catalog", o.Catalog != nil, func() string { return o.Catalog.File }},
		} {
			if e.set && e.file() == "" {
				return nil, fmt.Errorf("%s: module %s: output %s has no file (it would generate nothing and verify nothing)", path, mc.Definition, e.name)
			}
		}
		if seen[mc.Definition] {
			return nil, fmt.Errorf("%s: definition %s listed twice", path, mc.Definition)
		}
		seen[mc.Definition] = true
	}
	return &c, nil
}

// Path resolves a config-relative path.
func (c *Config) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}
