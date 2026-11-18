// Package live is `obs live`: read-only commands that pull numbers from a running
// observability stack (the local sandbox, dev, stage or prod) and compare them with what the
// repo declares. It installs, writes and deletes nothing on the target: client.go refuses
// every request that is not a query.
package live

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is one entry of obs.yaml targets: as written. Headers maps a request header name to the
// NAME of the environment variable that holds its value; the file never holds a secret.
type Spec struct {
	Metrics, Logs, Traces, Grafana string
	Headers                        map[string]string
	Line                           int
}

// Target is a resolved target: URLs and the header values read from the environment.
type Target struct {
	Name                           string
	Metrics, Logs, Traces, Grafana string
	Headers                        map[string]string
}

// Local sandbox addresses (obs sandbox up with the default port base).
var localDefaults = map[string]string{
	"metrics": "http://127.0.0.1:8428", "logs": "http://127.0.0.1:9428",
	"traces": "http://127.0.0.1:3200", "grafana": "http://127.0.0.1:3000",
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// tokenPrefixes are the starts of well-known credentials that happen to look like an environment
// variable name (Grafana service accounts and Cloud tokens, GitHub tokens, AWS key ids, JWTs).
var tokenPrefixes = []string{"glsa_", "glc_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "AKIA", "ASIA", "AIza", "eyJ"}

// maxEnvName is the longest environment variable name accepted; a real name is short, a token is not.
const maxEnvName = 64

// envNameProblem says why v cannot be the NAME of an environment variable, or "" when it can.
// The answer never repeats v: v may be a secret that someone wrote into the file.
func envNameProblem(v string) string {
	switch {
	case !envNameRe.MatchString(v):
		return "it is not a bare environment variable name"
	case len(v) > maxEnvName:
		return fmt.Sprintf("it is longer than %d characters, which no environment variable name is", maxEnvName)
	}
	for _, p := range tokenPrefixes {
		if strings.HasPrefix(v, p) {
			return "it starts like an access token (" + p + "...)"
		}
	}
	return ""
}

var userinfoRe = regexp.MustCompile(`://[^/?#]*@`)

// redactURL hides the user:password part of a URL before it goes into a message.
func redactURL(s string) string { return userinfoRe.ReplaceAllString(s, "://***@") }

type specFail func(line int, format string, args ...any) error

func failer(path string) specFail {
	return func(line int, format string, args ...any) error {
		if line > 0 {
			return fmt.Errorf("%s:%d: %s", path, line, fmt.Sprintf(format, args...))
		}
		return fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
	}
}

func checkURL(fail specFail, key string, n *yaml.Node) (string, error) {
	u, err := url.Parse(n.Value)
	if n.Kind != yaml.ScalarNode || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fail(n.Line, "%s: %q is not an http:// or https:// URL", key, redactURL(n.Value))
	}
	switch {
	case u.User != nil:
		return "", fail(n.Line, "%s: the URL carries a user name or password; put the credential in headers: as an environment variable name (the value is not repeated here)", key)
	case u.RawQuery != "" || u.ForceQuery || strings.Contains(n.Value, "?"):
		return "", fail(n.Line, "%s: the URL has a query string; write the bare address, obs adds the query endpoints itself", key)
	case u.Fragment != "" || strings.Contains(n.Value, "#"):
		return "", fail(n.Line, "%s: the URL has a fragment (#...); write the bare address", key)
	}
	return strings.TrimRight(n.Value, "/"), nil
}

// ParseTargets reads the targets: block of obs.yaml. A nil node is no targets.
func ParseTargets(n *yaml.Node, path string) (map[string]Spec, error) {
	fail := failer(path)
	out := map[string]Spec{}
	if n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		return out, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fail(n.Line, "targets: want a mapping of target name to its store URLs")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		name, body := n.Content[i], n.Content[i+1]
		base := "targets." + name.Value
		if body.Kind != yaml.MappingNode {
			return nil, fail(body.Line, "%s: want a mapping with metrics, logs, traces, grafana, headers", base)
		}
		sp := Spec{Line: name.Line}
		for j := 0; j+1 < len(body.Content); j += 2 {
			k, v := body.Content[j], body.Content[j+1]
			key := base + "." + k.Value
			switch k.Value {
			case "metrics", "logs", "traces", "grafana":
				u, err := checkURL(fail, key, v)
				if err != nil {
					return nil, err
				}
				switch k.Value {
				case "metrics":
					sp.Metrics = u
				case "logs":
					sp.Logs = u
				case "traces":
					sp.Traces = u
				case "grafana":
					sp.Grafana = u
				}
			case "headers":
				if v.Kind != yaml.MappingNode {
					return nil, fail(v.Line, "%s: want header name: ENVIRONMENT_VARIABLE_NAME", key)
				}
				sp.Headers = map[string]string{}
				for h := 0; h+1 < len(v.Content); h += 2 {
					hk, hv := v.Content[h], v.Content[h+1]
					if hv.Kind != yaml.ScalarNode {
						return nil, fail(hv.Line, "%s.%s: the value must be the NAME of an environment variable that holds the header value (a secret is never written in the file)", key, hk.Value)
					}
					if why := envNameProblem(hv.Value); why != "" {
						return nil, fail(hv.Line, "%s.%s: the value must be the NAME of an environment variable that holds the header value (a secret is never written in the file); %s, so it is refused and not printed", key, hk.Value, why)
					}
					sp.Headers[hk.Value] = hv.Value
				}
			default:
				return nil, fail(k.Line, "unknown key %s (known here: metrics, logs, traces, grafana, headers)", key)
			}
		}
		out[name.Value] = sp
	}
	return out, nil
}

// Resolve picks target name. For `local`, a key the file does not set falls back to the running
// sandbox (sandbox maps vm, vl, tempo, grafana to base URLs; may be nil), then to the default
// sandbox addresses. Header values come from getenv; an unset variable is an error.
func Resolve(name string, specs map[string]Spec, sandbox map[string]string, getenv func(string) string, path string) (*Target, error) {
	fail := failer(path)
	sp, found := specs[name]
	if !found && name != "local" {
		if len(specs) == 0 {
			return nil, fmt.Errorf("target %q: no targets: block in obs.yaml (write targets.%s.metrics, logs, traces and grafana in <dir>/obs.yaml)", name, name)
		}
		var known []string
		for k := range specs {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("target %q is not in %s (known targets: %s, and local)", name, path, strings.Join(known, ", "))
	}
	t := &Target{Name: name, Metrics: sp.Metrics, Logs: sp.Logs, Traces: sp.Traces, Grafana: sp.Grafana, Headers: map[string]string{}}
	if name == "local" {
		pick := func(have *string, sbKey, defKey string) {
			if *have != "" {
				return
			}
			if v := sandbox[sbKey]; v != "" {
				*have = strings.TrimRight(v, "/")
				return
			}
			*have = localDefaults[defKey]
		}
		pick(&t.Metrics, "vm", "metrics")
		pick(&t.Logs, "vl", "logs")
		pick(&t.Traces, "tempo", "traces")
		pick(&t.Grafana, "grafana", "grafana")
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	hs := make([]string, 0, len(sp.Headers))
	for h := range sp.Headers {
		hs = append(hs, h)
	}
	sort.Strings(hs)
	for _, h := range hs {
		if why := envNameProblem(sp.Headers[h]); why != "" {
			return nil, fail(sp.Line, "targets.%s.headers.%s: the value must be the NAME of an environment variable (a secret is never written in the file); %s, so it is refused and not printed", name, h, why)
		}
		v := getenv(sp.Headers[h])
		if v == "" {
			return nil, fail(sp.Line, "targets.%s.headers.%s: environment variable %s is not set", name, h, sp.Headers[h])
		}
		t.Headers[h] = v
	}
	return t, nil
}

// Estimates are the log and trace volumes the service is expected to send (obs.yaml
// estimates:). The contract holds series budgets only; volume has no other home.
type Estimates struct {
	Logs struct {
		LinesPerMin float64 `json:"lines_per_min,omitempty"`
		BytesPerMin float64 `json:"bytes_per_min,omitempty"`
	} `json:"logs"`
	Traces struct {
		SpansPerMin float64 `json:"spans_per_min,omitempty"`
		BytesPerMin float64 `json:"bytes_per_min,omitempty"`
	} `json:"traces"`
}

// ParseEstimates reads the estimates: block. A nil node is no estimates.
func ParseEstimates(n *yaml.Node, path string) (Estimates, error) {
	var e Estimates
	fail := failer(path)
	if n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		return e, nil
	}
	if n.Kind != yaml.MappingNode {
		return e, fail(n.Line, "estimates: want a mapping with logs and traces")
	}
	dst := map[string]map[string]*float64{
		"logs":   {"lines_per_min": &e.Logs.LinesPerMin, "bytes_per_min": &e.Logs.BytesPerMin},
		"traces": {"spans_per_min": &e.Traces.SpansPerMin, "bytes_per_min": &e.Traces.BytesPerMin},
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		keys, ok := dst[k.Value]
		if !ok {
			return e, fail(k.Line, "unknown key estimates.%s (known here: logs, traces)", k.Value)
		}
		if v.Kind != yaml.MappingNode {
			return e, fail(v.Line, "estimates.%s: want a mapping", k.Value)
		}
		for j := 0; j+1 < len(v.Content); j += 2 {
			kk, vv := v.Content[j], v.Content[j+1]
			p, ok := keys[kk.Value]
			if !ok {
				return e, fail(kk.Line, "unknown key estimates.%s.%s", k.Value, kk.Value)
			}
			var f float64
			if err := vv.Decode(&f); err != nil || f < 0 {
				return e, fail(vv.Line, "estimates.%s.%s: %q is not a number of 0 or more", k.Value, kk.Value, vv.Value)
			}
			*p = f
		}
	}
	return e, nil
}
