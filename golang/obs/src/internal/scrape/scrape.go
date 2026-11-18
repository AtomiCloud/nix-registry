// Package scrape pulls a Prometheus text endpoint (a local app's /metrics) and writes it
// into the sandbox's VictoriaMetrics, so a person can look at the app's own metrics next
// to the seed in Grafana.
//
// Concept section 11.8 names `obs scrape` next to the sandbox but does not define it.
// This is the minimal reading (an open point in the PR): one or more targets, scraped on
// an interval, written through VictoriaMetrics' /api/v1/import/prometheus with the labels
// given on the command line. Scraped metrics carry their where-from labels as
// durian_cloud_* (concept section 2.1), so pass them with --label. A vminsert labelmap (regex
// durian_cloud_(landscape|platform|service|module) -> durian.cloud.$1) turns them into the stored dotted labels in the real
// stack; the sandbox VictoriaMetrics has the same labelmap (sandbox.VMRelabelConfig).
package scrape

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Options is one scrape run.
type Options struct {
	Targets  []string          // http(s) URLs of Prometheus text endpoints
	VM       string            // base URL of the sandbox VictoriaMetrics
	Labels   map[string]string // added to every sample (extra_label)
	Job      string            // the job label (default obs-scrape)
	Interval time.Duration     // between scrapes (default 15s)
	Count    int               // scrape rounds; 0 = until ctx ends
	HTTP     *http.Client
	Out      io.Writer
}

const maxBody = 64 << 20

// Run scrapes every target each round. It returns an error when a round fails in Count>0
// mode (a one-shot scrape must tell its caller); the endless mode reports and goes on.
func Run(ctx context.Context, o Options) error {
	if len(o.Targets) == 0 {
		return fmt.Errorf("no target: give --target <url>")
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Job == "" {
		o.Job = "obs-scrape"
	}
	if o.Interval <= 0 {
		o.Interval = 15 * time.Second
	}
	for _, t := range o.Targets {
		u, err := url.Parse(t)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("target %q: want an http(s) URL", t)
		}
	}
	var failed error
	for round := 1; ; round++ {
		for _, t := range o.Targets {
			n, err := once(ctx, o, t)
			if err != nil {
				fmt.Fprintf(o.Out, "scrape %s: %v\n", t, err)
				failed = err
				continue
			}
			fmt.Fprintf(o.Out, "scraped %s: %d samples -> %s\n", t, n, o.VM)
		}
		if o.Count > 0 && round >= o.Count {
			return failed
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.Interval):
		}
	}
}

func once(ctx context.Context, o Options, target string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "text/plain;version=0.0.4")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return 0, err
	}
	if len(body) > maxBody {
		return 0, fmt.Errorf("response larger than %d MiB", maxBody>>20)
	}
	u, _ := url.Parse(target)
	labels := map[string]string{"job": o.Job, "instance": u.Host}
	for k, v := range o.Labels {
		labels[k] = v
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	q := url.Values{}
	for _, k := range keys {
		q.Add("extra_label", k+"="+labels[k])
	}
	post, err := http.NewRequestWithContext(ctx, http.MethodPost, o.VM+"/api/v1/import/prometheus?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	post.Header.Set("Content-Type", "text/plain")
	pr, err := o.HTTP.Do(post)
	if err != nil {
		return 0, err
	}
	defer pr.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(pr.Body, 1<<20))
	if pr.StatusCode/100 != 2 {
		return 0, fmt.Errorf("VictoriaMetrics HTTP %d: %s", pr.StatusCode, strings.TrimSpace(string(rb)))
	}
	return countSamples(body), nil
}

// countSamples counts the sample lines (not comments, not blank).
func countSamples(b []byte) int {
	n := 0
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" && !strings.HasPrefix(ln, "#") {
			n++
		}
	}
	return n
}
