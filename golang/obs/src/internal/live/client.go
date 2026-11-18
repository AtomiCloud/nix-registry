package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Read-only guarantee. Every request obs live sends goes through ReadOnly first: only GET and
// POST, and only the query endpoints of the stores (a POST is only ever the form encoding of a
// query that is too long for a URL). Anything else is refused before the network is touched,
// and a test asserts that a fake store never receives another method or path.
var (
	// metrics (VictoriaMetrics / Prometheus), logs (VictoriaLogs), traces (Tempo): GET or POST.
	queryPaths = regexp.MustCompile(`^(/api/v1/(query|query_range|series|labels|label/[A-Za-z0-9_.:-]+/values|status/tsdb|status/buildinfo)` +
		`|/select/logsql/(query|stats_query|stats_query_range|hits|streams|stream_ids|field_names|field_values)` +
		`|/api/(v2/search|metrics/query_range|metrics/query|status/buildinfo)` +
		`|/health|/ready)$`)
	// Grafana: GET only.
	grafanaPaths = regexp.MustCompile(`^/api/(health|search|annotations|dashboards/uid/[A-Za-z0-9_-]+|prometheus/grafana/api/v1/(alerts|rules)|v1/provisioning/alert-rules)$`)
)

// ReadOnly says whether obs live may send method to path. The error names both.
func ReadOnly(method, path string) error {
	if strings.Contains(path, "..") || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("read-only guard: refused %s %s (not a plain query path)", method, path)
	}
	switch method {
	case http.MethodGet:
		if queryPaths.MatchString(path) || grafanaPaths.MatchString(path) {
			return nil
		}
	case http.MethodPost:
		if queryPaths.MatchString(path) {
			return nil
		}
	}
	return fmt.Errorf("read-only guard: refused %s %s (obs live only sends GET or POST to the stores' query endpoints)", method, path)
}

// Client is the one HTTP path every live command uses.
type Client struct {
	Headers map[string]string
	HTTP    *http.Client // default: a client with a 30 s timeout
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

const maxBody = 64 << 20

// GetJSON GETs base+path?query and decodes the JSON answer into out.
func (c *Client) GetJSON(ctx context.Context, base, path string, q url.Values, out any) error {
	b, err := c.Get(ctx, base, path, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("GET %s%s: not the JSON expected: %v", base, path, err)
	}
	return nil
}

// Get GETs base+path?query and returns the body.
func (c *Client) Get(ctx context.Context, base, path string, q url.Values) ([]byte, error) {
	if err := ReadOnly(http.MethodGet, path); err != nil {
		return nil, err
	}
	if base == "" {
		return nil, fmt.Errorf("GET %s: the target has no URL for this store (set it under targets: in obs.yaml)", path)
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s%s: %v", base, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("GET %s%s: %v", base, path, err)
	}
	if resp.StatusCode/100 != 2 {
		body := strings.TrimSpace(string(b))
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		return nil, fmt.Errorf("GET %s%s: HTTP %d: %s", base, path, resp.StatusCode, body)
	}
	return b, nil
}
