package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	if strings.Contains(path, "..") || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#%\\ \x00") {
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

// baseURL parses a target address and refuses anything that is not a plain http(s) address: user
// names and passwords (Go turns them into Basic Auth), a query string or a fragment. The error
// never repeats the address, which may hold a secret.
func baseURL(base string) (*url.URL, error) {
	u, err := url.Parse(base)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "":
		return nil, fmt.Errorf("read-only guard: the target address is not a plain http:// or https:// URL")
	case u.User != nil:
		return nil, fmt.Errorf("read-only guard: the target address carries a user name or password; use headers: with an environment variable name")
	case u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") || u.Fragment != "":
		return nil, fmt.Errorf("read-only guard: the target address has a query string or a fragment")
	}
	return u, nil
}

// checkHop runs the guard on a URL the client is about to request: the request URL itself and
// every redirect hop. It must stay on the target's scheme and host, carry no credentials, and be
// a query path under the target's own path prefix.
func checkHop(method string, base, hop *url.URL) error {
	switch {
	case hop.Scheme != base.Scheme || hop.Host != base.Host:
		return fmt.Errorf("redirect refused: it leaves the target's host (to %s); obs live never follows one", hop.Host)
	case hop.User != nil:
		return fmt.Errorf("redirect refused: it carries credentials")
	}
	prefix := strings.TrimRight(base.Path, "/")
	if !strings.HasPrefix(hop.Path, prefix+"/") {
		return fmt.Errorf("redirect refused: it leaves the target's path prefix")
	}
	rel := strings.TrimPrefix(hop.Path, prefix)
	if err := ReadOnly(method, rel); err != nil {
		return fmt.Errorf("redirect refused: the guard does not allow a %s there", method)
	}
	return nil
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
	bu, err := baseURL(base)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %v", path, err)
	}
	final := *bu
	final.Path = strings.TrimRight(bu.Path, "/") + path
	final.RawPath = ""
	final.RawQuery = q.Encode()
	// The guard runs on the URL as it will be sent, not only on the path argument.
	if err := checkHop(http.MethodGet, bu, &final); err != nil {
		return nil, err
	}
	base = bu.Scheme + "://" + bu.Host + strings.TrimRight(bu.Path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, final.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	hc := *c.http() // a copy: the guard below is set for this request whatever client the caller gave
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("redirect refused: more than 5 redirects")
		}
		return checkHop(r.Method, bu, r.URL)
	}
	resp, err := hc.Do(req)
	if err != nil {
		// net/http wraps the error with the URL it was fetching: after a redirect that is the
		// Location the store sent (a host, a path, a query that may carry a token). Say only what failed.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("GET %s%s: %v", base, path, safeDoError(err))
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

// safeDoError is what obs prints for an error of http.Client.Do (url.Error already unwrapped).
// It is an allowlist: obs's own redirect refusals and network or timeout errors (they name an
// address at most) are printed as they are; anything else may quote what the store sent (net/http
// quotes an unparsable Location whole, textproto quotes a malformed header line whole, a Location
// may carry a token), so it is replaced by a fixed text that prints nothing of the response.
func safeDoError(err error) error {
	msg := err.Error()
	var netErr net.Error
	switch {
	case strings.HasPrefix(msg, "redirect refused"):
		return err
	case strings.Contains(msg, "Location header"):
		return errors.New("redirect refused: the store sent a Location obs cannot parse")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.As(err, &netErr) && !strings.Contains(msg, "malformed"):
		return err
	}
	return errors.New("the store sent a response obs cannot read (malformed HTTP); nothing of it is printed")
}
