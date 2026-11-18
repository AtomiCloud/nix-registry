package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func get(ctx context.Context, hc *http.Client, url string, auth bool) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if auth {
		req.SetBasicAuth("admin", "admin")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return b, resp.StatusCode, err
}

func probe(ctx context.Context, hc *http.Client, url string) error {
	_, code, err := get(ctx, hc, url, false)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, code)
	}
	return nil
}

// waitReady polls every component's health path until all answer 200 or ctx ends.
func waitReady(ctx context.Context, hc *http.Client, urls map[string]string, comps []Component) error {
	for _, k := range comps {
		var last error
		for {
			last = probe(ctx, hc, urls[k.Name]+k.Health)
			if last == nil {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s did not become ready: %w", k.Name, last)
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

// The version every Victoria binary puts on its log counters: app_version="victoria-metrics-<date>-tags-v1.156.0-0-g<sha>".
var appVersionRe = regexp.MustCompile(`app_version="victoria-[a-z-]+?-\d+-\d+-tags-(v\d+\.\d+\.\d+)-`)

// VerifyVersions asks each component which version it runs and refuses when it is not the
// pin. Pyroscope has no version endpoint obs relies on: its image tag is the pin.
func VerifyVersions(ctx context.Context, hc *http.Client, urls map[string]string) error {
	return verifyVersions(ctx, hc, urls, Pins)
}

// verifyVersions is VerifyVersions against the given stack (the pins, or what obs.yaml names).
func verifyVersions(ctx context.Context, hc *http.Client, urls map[string]string, comps []Component) error {
	byName := func(name string) (Component, bool) {
		for _, c := range comps {
			if c.Name == name {
				return c, true
			}
		}
		return Component{}, false
	}
	var bad []string
	check := func(name, want, got string, err error) {
		switch {
		case err != nil:
			bad = append(bad, fmt.Sprintf("%s: cannot read its version: %v", name, err))
		case strings.TrimPrefix(got, "v") != strings.TrimPrefix(want, "v"):
			bad = append(bad, fmt.Sprintf("%s runs %q, the pin is %s", name, got, want))
		}
	}
	metricsLine := func(name string) (string, error) {
		b, code, err := get(ctx, hc, urls[name]+"/metrics", false)
		if err == nil && code != http.StatusOK {
			err = fmt.Errorf("HTTP %d", code)
		}
		if err != nil {
			return "", err
		}
		m := appVersionRe.FindStringSubmatch(string(b))
		if m == nil {
			return "", nil
		}
		return m[1], nil
	}
	vm, _ := byName("vm")
	line, err := metricsLine("vm")
	check("VictoriaMetrics", vm.Tag, line, err)
	vl, _ := byName("vl")
	line, err = metricsLine("vl")
	check("VictoriaLogs", vl.Tag, line, err)

	jsonField := func(url string, auth bool, path ...string) (string, error) {
		b, code, err := get(ctx, hc, url, auth)
		if err == nil && code != http.StatusOK {
			err = fmt.Errorf("HTTP %d", code)
		}
		if err != nil {
			return "", err
		}
		var m any
		if err := json.Unmarshal(b, &m); err != nil {
			return "", err
		}
		for _, p := range path {
			mm, _ := m.(map[string]any)
			m = mm[p]
		}
		s, _ := m.(string)
		return s, nil
	}
	gf, _ := byName("grafana")
	v, err := jsonField(urls["grafana"]+"/api/health", false, "version")
	check("Grafana", gf.Tag, v, err)
	v, err = jsonField(urls["grafana"]+"/api/plugins/"+PluginID+"/settings", true, "info", "version")
	check("Grafana plugin "+PluginID, PluginVersion, v, err)
	tp, _ := byName("tempo")
	v, err = jsonField(urls["tempo"]+"/api/status/buildinfo", false, "version")
	check("Tempo", tp.Tag, v, err)
	if len(bad) > 0 {
		return fmt.Errorf("refusing: a component does not match its pin: %s; run `obs sandbox down` and up again", strings.Join(bad, "; "))
	}
	return nil
}
