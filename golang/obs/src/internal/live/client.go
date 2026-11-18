package live

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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
		return &redirectRefused{"it leaves the target's host; obs live never follows one"}
	case hop.User != nil:
		return &redirectRefused{"it carries credentials"}
	}
	prefix := strings.TrimRight(base.Path, "/")
	if !strings.HasPrefix(hop.Path, prefix+"/") {
		return &redirectRefused{"it leaves the target's path prefix"}
	}
	rel := strings.TrimPrefix(hop.Path, prefix)
	if err := ReadOnly(method, rel); err != nil {
		return &redirectRefused{"the guard does not allow a " + method + " there"}
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
		return fmt.Errorf("GET %s%s: not the JSON expected (obs prints no part of the answer)", base, path)
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
			return &redirectRefused{"more than 5 redirects"}
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
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("GET %s%s: reading the response: the store closed the connection mid-response", base, path)
		}
		return nil, fmt.Errorf("GET %s%s: reading the response: %v", base, path, safeDoError(err))
	}
	if resp.StatusCode/100 != 2 {
		body := c.storeText(string(b))
		return nil, fmt.Errorf("GET %s%s: HTTP %d: %s", base, path, resp.StatusCode, body)
	}
	return b, nil
}

// redirectRefused is the guard's refusal of a hop; its reasons are fixed texts written in checkHop,
// never a URL or anything else the store sent.
type redirectRefused struct{ reason string }

func (e *redirectRefused) Error() string { return "redirect refused: " + e.reason }

// safeDoError is what obs prints for an error of http.Client.Do or of reading a response body
// (url.Error already unwrapped). obs never prints Go's or the store's own error text: net/http
// quotes an unparsable Location whole, textproto quotes a malformed header or trailer line whole,
// a deadline error can carry the transport error before it, a certificate error names the
// certificate's hosts. Typed causes are checked first, each with a fixed text written here; the
// one text match (net/http's untyped Location parse error) comes last; anything else is one
// neutral fixed text.
func safeDoError(err error) error {
	var refused *redirectRefused
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalidErr x509.CertificateInvalidError
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &refused):
		return refused
	case errors.Is(err, context.Canceled):
		return errors.New("the request was cancelled")
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return errors.New("the request timed out")
	case errors.As(err, &unknownCA):
		return errors.New("TLS: the store's certificate is signed by an authority this machine does not trust")
	case errors.As(err, &hostErr):
		return errors.New("TLS: the store's certificate is not valid for the target's host name")
	case errors.As(err, &invalidErr):
		return errors.New("TLS: the store's certificate is invalid (expired, not yet valid, or not for a server)")
	case errors.As(err, &certErr):
		return errors.New("TLS: the store's certificate could not be verified")
	case errors.Is(err, http.ErrSchemeMismatch):
		return errors.New("the target URL is https but the store answered plain HTTP")
	case errors.As(err, &recordErr):
		return errors.New("TLS: the store's answer is not TLS (a plain-HTTP port or a broken TLS endpoint)")
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("the connection ended early")
	case errors.As(err, &dnsErr):
		return errors.New("the target's host name could not be resolved (DNS)")
	case errors.Is(err, syscall.ECONNREFUSED):
		return errors.New("connection refused: nothing listens at the target address")
	case errors.Is(err, syscall.ECONNRESET):
		return errors.New("the store reset the connection")
	case strings.Contains(err.Error(), "failed to parse Location header"):
		return &redirectRefused{"the store sent a Location obs cannot parse"}
	}
	return errors.New("the request failed (obs prints no part of the underlying error)")
}

// storeText is how obs prints text the store wrote itself (a non-2xx body, a query API error):
// it is the main diagnostic, so it is kept, but every value obs sent in a header (4+ chars, and each long
// word of it, for "Bearer <token>") is replaced by ***, since auth proxies echo the credential
// they refused, and control characters (a terminal escape) become spaces. At most 200 runes.
func (c *Client) storeText(s string) string {
	for _, v := range c.Headers {
		if len(v) < 4 { // a short value (a tenant id "1") would garble the text; it is no secret
			continue
		}
		// The whole value and each word of it (a gateway may echo only the token of "Bearer <token>",
		// or rewrite the scheme: vmauth turns "Token x" into "Bearer x"), each as written and as Go's
		// %q writes it (vmauth quotes the token it refused: backslash escapes).
		parts := []string{v}
		for _, w := range strings.Fields(v) {
			if len(w) >= 4 {
				parts = append(parts, w)
			}
		}
		for _, p := range parts {
			q := strconv.Quote(p)
			for _, form := range []string{q[1 : len(q)-1], p} {
				s = strings.ReplaceAll(s, form, "***")
			}
		}
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if rs := []rune(s); len(rs) > 200 {
		s = string(rs[:197]) + "..." // 200 runes with the ellipsis
	}
	return s
}
