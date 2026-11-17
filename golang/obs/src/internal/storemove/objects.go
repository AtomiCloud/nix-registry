package storemove

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// object is one Kubernetes object of a manifest, after kubectl's List flattening.
type object struct {
	Kind, Name string
	Raw        map[string]any
}

func (o object) key() string { return canonicalKind(o.Kind) + "/" + o.Name }

// kindAPIVersion is every Kind a store-move manifest may name, with the one apiVersion
// kubectl must resolve it to. The Kind spelling must be exact (no alias, no lower case)
// and apiVersion, when present, must match: a custom resource that merely reuses the
// Kind name (for example a CRD "Service" in another group) is refused.
var kindAPIVersion = map[string]string{
	"StatefulSet": "apps/v1", "Deployment": "apps/v1", "DaemonSet": "apps/v1",
	"Service": "v1", "ConfigMap": "v1", "Secret": "v1", "ServiceAccount": "v1", "PersistentVolumeClaim": "v1",
	"Endpoints": "v1", "EndpointSlice": "discovery.k8s.io/v1",
	"Role": "rbac.authorization.k8s.io/v1", "RoleBinding": "rbac.authorization.k8s.io/v1",
}

// kubectlResource is the fully qualified kubectl resource for a live read, so a CRD
// with the same short name cannot shadow the built-in kind.
func kubectlResource(o object) string {
	switch o.Kind {
	case "StatefulSet":
		return "statefulset.apps/" + o.Name
	case "Deployment":
		return "deployment.apps/" + o.Name
	case "DaemonSet":
		return "daemonset.apps/" + o.Name
	case "Role":
		return "role.rbac.authorization.k8s.io/" + o.Name
	case "RoleBinding":
		return "rolebinding.rbac.authorization.k8s.io/" + o.Name
	}
	return strings.ToLower(o.Kind) + "/" + o.Name
}

func mapAt(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		next, _ := m[k].(map[string]any)
		if next == nil {
			return nil
		}
		m = next
	}
	return m
}

// parseManifest reads every object of a manifest the way kubectl apply sees it and
// refuses the shapes where kubectl and this check could disagree:
//   - any document carrying `items` is a list to kubectl (unstructured decoding turns
//     every object with a nonempty items array into a List, whatever its kind), so
//     items is accepted only on kind List and flattened; on any other kind it is refused;
//   - a missing kind, a Kind outside kindAPIVersion (exact spelling) or another apiVersion;
//   - a missing name, generateName, or a namespace other than the configured one;
//   - the same object twice (the later copy would win at apply time).
func parseManifest(file, namespace string) ([]object, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var out []object
	seen := map[string]bool{}
	var add func(m map[string]any, where string) error
	add = func(m map[string]any, where string) error {
		kind, _ := m["kind"].(string)
		apiVersion, hasAPI := m["apiVersion"]
		if _, ok := m["items"]; ok || kind == "List" {
			if kind != "List" {
				return fmt.Errorf("%s: %s %q carries items; kubectl would apply them as a list, refusing", file, where, kind)
			}
			if hasAPI && apiVersion != "v1" {
				return fmt.Errorf("%s: %s List apiVersion %v, want v1", file, where, apiVersion)
			}
			items, ok := m["items"].([]any)
			if !ok {
				return fmt.Errorf("%s: %s List without an items array", file, where)
			}
			for i, item := range items {
				im, ok := item.(map[string]any)
				if !ok {
					return fmt.Errorf("%s: %s items[%d] is not an object", file, where, i)
				}
				if err := add(im, fmt.Sprintf("%s.items[%d]", where, i)); err != nil {
					return err
				}
			}
			return nil
		}
		want, known := kindAPIVersion[kind]
		if !known {
			return fmt.Errorf("%s: %s kind %q is not an allowed kind (exact spelling; allowed: %s)", file, where, kind, strings.Join(sortedKeys(kindAPIVersion), ", "))
		}
		if hasAPI && apiVersion != want {
			return fmt.Errorf("%s: %s %s apiVersion %v, want %s", file, where, kind, apiVersion, want)
		}
		meta, _ := m["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			return fmt.Errorf("%s: %s %s without metadata.name", file, where, kind)
		}
		if _, ok := meta["generateName"]; ok {
			return fmt.Errorf("%s: %s %s/%s uses generateName", file, where, kind, name)
		}
		if ns, ok := meta["namespace"]; ok && ns != namespace {
			return fmt.Errorf("%s: %s %s/%s namespace %v, want the configured %q", file, where, kind, name, ns, namespace)
		}
		o := object{Kind: kind, Name: name, Raw: m}
		if seen[o.key()] {
			return fmt.Errorf("%s: %s appears twice", file, o.key())
		}
		seen[o.key()] = true
		out = append(out, o)
		return nil
	}
	for doc := 0; ; doc++ {
		var m map[string]any
		if err = dec.Decode(&m); err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if m == nil {
			continue
		}
		if err = add(m, fmt.Sprintf("document %d", doc+1)); err != nil {
			return nil, err
		}
	}
}

// scalar renders a YAML or JSON scalar the same way for both (YAML 2 is int, JSON 2
// is float64; both print "2").
func scalar(v any) string { return fmt.Sprint(v) }

func brief(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

// within reports the first path at which sub is not contained in sup: every map key of
// sub must exist in sup with a contained value, every list must have the same length
// and contained elements in the same order, every scalar must be equal. A null in sub
// (kubectl apply: delete the field) only matches a null. skip names paths that may
// differ (the declared conversion fields).
func within(sub, sup any, path string, skip func(string) bool) error {
	if skip != nil && skip(path) {
		return nil
	}
	switch s := sub.(type) {
	case map[string]any:
		m, ok := sup.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: %s vs %s", path, brief(sub), brief(sup))
		}
		for _, k := range sortedKeys(s) {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if skip != nil && skip(p) {
				continue
			}
			v, ok := m[k]
			if !ok {
				return fmt.Errorf("%s: %s vs absent", p, brief(s[k]))
			}
			if err := within(s[k], v, p, skip); err != nil {
				return err
			}
		}
		return nil
	case []any:
		l, ok := sup.([]any)
		if !ok || len(l) != len(s) {
			return fmt.Errorf("%s: %s vs %s", path, brief(sub), brief(sup))
		}
		for i := range s {
			if err := within(s[i], l[i], fmt.Sprintf("%s[%d]", path, i), skip); err != nil {
				return err
			}
		}
		return nil
	}
	switch sup.(type) {
	case map[string]any, []any:
		return fmt.Errorf("%s: %s vs %s", path, brief(sub), brief(sup))
	}
	if (sub == nil) != (sup == nil) || scalar(sub) != scalar(sup) {
		return fmt.Errorf("%s: %s vs %s", path, brief(sub), brief(sup))
	}
	return nil
}

// argToken is one command-line argument read the way Go's flag package (and so
// VictoriaMetrics/VictoriaLogs lib/envflag) reads it.
type argToken struct {
	index   int    // position in the list
	flag    bool   // a well-formed flag inside the flag zone
	dashes  int    // 1 or 2
	name    string // flag name without dashes and value
	value   string
	inline  bool // -name=value (false: -name value, value at index+1)
	consume bool // the separate value at index+1 belongs to this flag
}

// tokenize reads args with Go flag semantics (Go flag/flag.go parseOne): an argument is a
// flag when it starts with "-" and has more than one character; exactly one or two leading
// dashes; "--" ends the flags; the first non-flag argument ends the flags, and
// VictoriaMetrics/VictoriaLogs then exit ("unprocessed command-line args left",
// lib/envflag/envflag.go:36-39); a name starting with "-" or "=" is "bad flag syntax".
// valueFlags names flags whose value, when not inline, is the next argument. It returns
// one token per flag and an error for any argument that names one of the guarded flags
// but is not a well-formed flag inside the flag zone (positional `storageNode=...`,
// `---storageNode=...`, `-storageNode` without a value, a guarded flag after `--`).
func tokenize(args []any, skipFirst bool, guarded ...string) ([]argToken, error) {
	isGuarded := func(s string) string {
		bare := strings.TrimLeft(s, "-")
		name, _, _ := strings.Cut(bare, "=")
		for _, g := range guarded {
			if name == g {
				return g
			}
		}
		return ""
	}
	var out []argToken
	zone := true
	start := 0
	if skipFirst {
		start = 1
	}
	for i := start; i < len(args); i++ {
		s, isString := args[i].(string)
		if !isString {
			zone = false
			continue
		}
		if !zone {
			if g := isGuarded(s); g != "" {
				return nil, fmt.Errorf("argument %d %q names -%s after flag parsing stopped; the store would not read it", i, s, g)
			}
			continue
		}
		if s == "--" {
			zone = false
			continue
		}
		if len(s) < 2 || s[0] != '-' {
			if g := isGuarded(s); g != "" {
				return nil, fmt.Errorf("argument %d %q is positional, not the -%s flag (Go flag syntax needs one or two leading dashes)", i, s, g)
			}
			zone = false
			continue
		}
		dashes := 1
		if s[1] == '-' {
			dashes = 2
		}
		name := s[dashes:]
		if name == "" || name[0] == '-' || name[0] == '=' {
			if g := isGuarded(s); g != "" {
				return nil, fmt.Errorf("argument %d %q: bad flag syntax for -%s (exactly one or two leading dashes)", i, s, g)
			}
			return nil, fmt.Errorf("argument %d %q: bad flag syntax (Go flag parseOne refuses it)", i, s)
		}
		t := argToken{index: i, flag: true, dashes: dashes, name: name}
		if n, v, ok := strings.Cut(name, "="); ok {
			t.name, t.value, t.inline = n, v, true
		}
		if !t.inline && isGuarded(s) != "" {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("argument %d %q: -%s without a value", i, s, t.name)
			}
			v, ok := args[i+1].(string)
			if !ok {
				return nil, fmt.Errorf("argument %d %q: -%s value is not a string", i, s, t.name)
			}
			t.value, t.consume = v, true
			i++
		}
		out = append(out, t)
	}
	return out, nil
}

// spelling is the exact form of a flag occurrence (dashes and inline/separate value),
// kept in the sentinel so a spelling change is a difference.
func (t argToken) spelling() string {
	sep := " "
	if t.inline {
		sep = "="
	}
	return strings.Repeat("-", t.dashes) + t.name + sep
}
