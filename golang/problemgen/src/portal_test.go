package main

// The error portal is a run-time value (owner decision, corpus PR 76): each pod reads
// errorPortal.url at start. Generated code carries no portal; the config's portal is optional.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedCodeHasNoPortal(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	cfg := &Config{Platform: "nitroso", Service: "zinc", Portal: "https://baked.example"}
	outs := map[string]string{
		"go":     GenerateGo(m, cfg, &GoOut{Package: "x", Runtime: "example.com/rt"}, "x"),
		"ts":     langs["typescript"].Generate(m, cfg, Outputs{TypeScript: &TSOut{Runtime: "r"}}, "x"),
		"csharp": langs["csharp"].Generate(m, cfg, Outputs{CSharp: &CSOut{Namespace: "N.Gen", Runtime: "R"}}, "x"),
		"dart":   GenerateDart(m, cfg, &DartOut{Runtime: "package:r/r.dart"}, "x"),
	}
	for lang, src := range outs {
		if strings.Contains(src, "baked.example") || strings.Contains(strings.ToLower(src), "portal") {
			t.Errorf("%s output mentions the portal:\n%s", lang, src)
		}
	}
}

func TestConfigPortalOptional(t *testing.T) {
	dir := t.TempDir()
	c := filepath.Join(dir, "cfg.yaml")
	body := "apiVersion: problems.atomi.cloud/v1\nkind: GeneratorConfig\nplatform: nitroso\nservice: zinc\n%smodules:\n  - definition: d.yaml\n    outputs: { record: { file: r.yaml } }\n"
	for _, tc := range []struct{ portal, want string }{
		{"", ""},
		{"portal: https://errors.atomi.cloud\n", ""},
		{"portal: https://errors.atomi.cloud/\n", "portal (optional) must be"},
		{"portal: errors.atomi.cloud\n", "portal (optional) must be"},
		{"portal: \"https://errors.atomi.cloud?x=1\"\n", "portal (optional) must be"},
		{"portal: \"https://errors.atomi.cloud#f\"\n", "portal (optional) must be"},
	} {
		os.WriteFile(c, []byte(strings.Replace(body, "%s", tc.portal, 1)), 0o644)
		_, err := LoadConfig(c)
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("portal line %q: err = %v, want %q", tc.portal, err, tc.want)
		}
	}
}

// The Helm record reads .Values.errorPortal.url (the value the pod reads). Without a config
// portal it is required; with one, that is the default. Either way it must be an https origin.
func TestRecordPortalFromValues(t *testing.T) {
	m, ds := loadSrc(t, r2Head+"problems:\n  p: { title: P, status: 400, recoverable: true, detail: d }\n")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	check := `{{- if not (regexMatch "` + portalOriginRe + `" $portal) }}{{ fail "problemgen: .Values.errorPortal.url must be an https origin`
	typ := `type: "{{ $portal }}/docs/{{ $landscape }}/nitroso/zinc/m/v1/p"`
	without := RenderRecordYAML(m, &Config{Platform: "nitroso", Service: "zinc"}, "", "", "")
	if !strings.Contains(without, `required "problemgen: .Values.errorPortal.url is required" $errorPortal.url`) || !strings.Contains(without, check) || !strings.Contains(without, typ) {
		t.Errorf("record without a config portal:\n%s", without)
	}
	with := RenderRecordYAML(m, &Config{Platform: "nitroso", Service: "zinc", Portal: "https://errors.atomi.cloud"}, "", "", "")
	if !strings.Contains(with, `default "https://errors.atomi.cloud" $errorPortal.url`) || !strings.Contains(with, check) || strings.Contains(with, "required \"problemgen: .Values.errorPortal") {
		t.Errorf("record with a config portal:\n%s", with)
	}
	lit := RenderRecordYAML(m, &Config{Platform: "nitroso", Service: "zinc"}, "lapras", "https://e.example", "")
	if !strings.Contains(lit, `type: "https://e.example/docs/lapras/nitroso/zinc/m/v1/p"`) || strings.Contains(lit, "{{") {
		t.Errorf("literal record:\n%s", lit)
	}
}
