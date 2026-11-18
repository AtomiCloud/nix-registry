package storemove

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// object is one Kubernetes object of the scaled manifest, after List flattening.
type object struct {
	Kind, Name string
	Raw        map[string]any
}

func (o object) key() string { return canonicalKind(o.Kind) + "/" + o.Name }

func canonicalKind(kind string) string {
	switch strings.ToLower(kind) {
	case "sts", "statefulset", "statefulsets", "statefulset.apps":
		return "statefulset"
	case "deploy", "deployment", "deployments", "deployment.apps":
		return "deployment"
	case "ds", "daemonset", "daemonsets", "daemonset.apps":
		return "daemonset"
	case "svc", "service", "services":
		return "service"
	case "pvc", "persistentvolumeclaim", "persistentvolumeclaims":
		return "persistentvolumeclaim"
	case "sa", "serviceaccount", "serviceaccounts":
		return "serviceaccount"
	case "cm", "configmap", "configmaps":
		return "configmap"
	case "role", "roles", "role.rbac.authorization.k8s.io":
		return "role"
	case "rolebinding", "rolebindings", "rolebinding.rbac.authorization.k8s.io":
		return "rolebinding"
	}
	return strings.ToLower(kind)
}

// canonicalResource turns a config reference such as sts/tempo into statefulset/tempo.
func canonicalResource(resource string) string {
	kind, name, ok := strings.Cut(resource, "/")
	if !ok {
		return resource
	}
	return canonicalKind(kind) + "/" + name
}

// qualified is the fully qualified kubectl resource, so a CRD with the same short name
// can never shadow the built-in kind.
func qualified(resource string) string {
	kind, name, _ := strings.Cut(canonicalResource(resource), "/")
	switch kind {
	case "statefulset", "deployment", "daemonset":
		return kind + ".apps/" + name
	case "role", "rolebinding":
		return kind + ".rbac.authorization.k8s.io/" + name
	}
	return kind + "/" + name
}

// kindAPIVersion is every Kind the scaled manifest may name, with the one apiVersion
// kubectl must resolve it to. The Kind spelling must be exact (no alias, no lower case);
// a custom resource that merely reuses the Kind name in another group is refused.
var kindAPIVersion = map[string]string{
	"StatefulSet": "apps/v1", "Deployment": "apps/v1",
	"Service": "v1", "ConfigMap": "v1", "ServiceAccount": "v1",
	"Role": "rbac.authorization.k8s.io/v1", "RoleBinding": "rbac.authorization.k8s.io/v1",
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

// parseManifest reads every object of the scaled manifest and refuses the shapes where
// kubectl and this check could disagree:
//   - any document carrying `items` is a list to kubectl, so items is accepted only on
//     kind List (apiVersion v1) and flattened; on any other kind it is refused. obs then
//     creates the flattened objects itself, never the user's List;
//   - a missing kind, a Kind outside kindAPIVersion (exact spelling) or another apiVersion;
//   - a missing name, generateName, or a namespace other than the configured one;
//   - the same object twice; a duplicate YAML key (yaml.v3 refuses it).
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
				return fmt.Errorf("%s: %s %q carries items; kubectl would read it as a list, refusing", file, where, kind)
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

// scalar renders a YAML or JSON scalar the same way for both (YAML 2 is int, JSON 2 is
// float64; both print "2"). Integral floats print exactly (1000000, never 1e+06).
func scalar(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	}
	return fmt.Sprint(v)
}

func brief(v any) string {
	s := scalar(v)
	if _, ok := v.(string); ok {
		s = strconv.Quote(s)
	} else if _, ok := v.(map[string]any); ok {
		s = fmt.Sprint(v)
	}
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

func stringMap(v any) map[string]string {
	m, _ := v.(map[string]any)
	out := map[string]string{}
	for k, x := range m {
		out[k] = scalar(x)
	}
	return out
}

// argToken is one command-line flag read the way Go's flag package (and so
// VictoriaMetrics/VictoriaLogs lib/envflag, Tempo, Pyroscope) reads it.
type argToken struct {
	index   int    // position in the list
	dashes  int    // 1 or 2
	name    string // flag name without dashes and value
	value   string
	inline  bool // -name=value (false: -name value, value at index+1)
	consume bool // the separate value at index+1 belongs to this flag
}

// tokenize reads args with Go flag semantics (Go flag/flag.go parseOne): an argument is a
// flag when it starts with "-" and has more than one character; exactly one or two leading
// dashes; "--" ends the flags; a positional argument ends the flags (VictoriaMetrics and
// VictoriaLogs then exit: "unprocessed command-line args left", lib/envflag/envflag.go);
// a name starting with "-" or "=" is "bad flag syntax".
//
// Whether an unguarded flag without "=" takes the next argument as its value depends on
// its type, which obs does not know. tokenize reads it as a value (so the unchanged valid
// `-memory.allowedPercent 60` is not a positional argument). This is sound for what obs
// reads: the args of a LIVE, Ready workload, whose binary already parsed them; a boolean
// flag followed by a positional argument would have made it exit.
//
// guarded flags (the ones obs reads or changes) must be well formed inside the flag zone;
// any other spelling of a guarded name (positional `storageNode=...`, `---storageNode=...`,
// a missing value, after `--`) is an error. skipFirst skips the binary in a command list.
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
			return nil, fmt.Errorf("argument %d is not a string", i)
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
		t := argToken{index: i, dashes: dashes, name: name}
		if n, v, ok := strings.Cut(name, "="); ok {
			t.name, t.value, t.inline = n, v, true
		}
		if !t.inline {
			next, isNext := "", i+1 < len(args)
			if isNext {
				next, _ = args[i+1].(string)
			}
			switch {
			case isGuarded(s) != "":
				if !isNext {
					return nil, fmt.Errorf("argument %d %q: -%s without a value", i, s, t.name)
				}
				if _, ok := args[i+1].(string); !ok {
					return nil, fmt.Errorf("argument %d %q: -%s value is not a string", i, s, t.name)
				}
				t.value, t.consume = next, true
				i++
			case isNext && !strings.HasPrefix(next, "-"):
				// A value-taking flag in a running binary (see above).
				t.value, t.consume = next, true
				i++
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// argSite is one args/command list of one container of a live workload.
type argSite struct {
	container int    // index in spec.template.spec.containers
	name      string // container name
	field     string // "args" or "command"
	args      []any
}

func (s argSite) path(i int) string {
	return pointer("spec", "template", "spec", "containers", s.container, s.field, i)
}

// argSites lists every args/command list of every (non-init) container.
func argSites(o map[string]any) []argSite {
	var out []argSite
	containers, _ := mapAt(o, "spec", "template", "spec")["containers"].([]any)
	for i, c := range containers {
		container, _ := c.(map[string]any)
		name, _ := container["name"].(string)
		for _, field := range []string{"command", "args"} {
			if args, ok := container[field].([]any); ok {
				out = append(out, argSite{i, name, field, args})
			}
		}
	}
	return out
}

// flagHit is one occurrence of a guarded flag in a live workload.
type flagHit struct {
	site argSite
	tok  argToken
}

// findFlags returns every occurrence of flag name in the containers of o (init
// containers are not the store) and refuses any malformed spelling of the guarded names.
func findFlags(o map[string]any, name string, guarded ...string) ([]flagHit, error) {
	var hits []flagHit
	for _, s := range argSites(o) {
		toks, err := tokenize(s.args, s.field == "command", append([]string{name}, guarded...)...)
		if err != nil {
			return nil, fmt.Errorf("containers[%d](%s).%s: %w", s.container, s.name, s.field, err)
		}
		for _, t := range toks {
			if t.name == name {
				hits = append(hits, flagHit{s, t})
			}
		}
	}
	return hits, nil
}

// setFlagValue returns the test+replace operations that change one flag occurrence's
// value, keeping its spelling (dashes, inline or separate value).
func setFlagValue(h flagHit, value string) []op {
	pin := op{"test", pointer("spec", "template", "spec", "containers", h.site.container, "name"), h.site.name}
	if h.tok.consume {
		return []op{pin,
			{"test", h.site.path(h.tok.index), h.site.args[h.tok.index]},
			{"test", h.site.path(h.tok.index + 1), h.tok.value},
			{"replace", h.site.path(h.tok.index + 1), value}}
	}
	old, _ := h.site.args[h.tok.index].(string)
	return []op{pin,
		{"test", h.site.path(h.tok.index), old},
		{"replace", h.site.path(h.tok.index), strings.Repeat("-", h.tok.dashes) + h.tok.name + "=" + value}}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// deepCopy copies a decoded YAML/JSON tree.
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
