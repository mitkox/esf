package cube

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// Mirror the SDK's data-plane routing while retaining control of response reads.
// Files().Read buffers the entire response and therefore cannot enforce a bound
// against a sandbox file replaced or grown after its inventory was produced.
func boundedFileHTTPClient(cfg cubesandbox.Config) (*http.Client, string) {
	port := cfg.ProxyPortHTTP
	if port <= 0 {
		port = DefaultProxyPortHTTP
	}
	scheme := cfg.ProxyScheme
	if scheme == "" {
		scheme = "http"
		if port == 443 {
			scheme = "https"
		}
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport.DialContext = dialer.DialContext
	if cfg.ProxyNodeIP != "" {
		target := net.JoinHostPort(cfg.ProxyNodeIP, strconv.Itoa(port))
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, target)
		}
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, scheme
}

func (s *cubeSandbox) ReadFileBounded(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if path == "" || maxBytes <= 0 || maxBytes == math.MaxInt64 || s.fileHTTP == nil || s.sb == nil {
		return nil, errors.New("invalid bounded sandbox file read")
	}
	target := url.URL{Scheme: s.fileScheme, Host: s.sb.GetHost(cubesandbox.EnvdPort), Path: "/files", RawQuery: url.Values{"path": []string{path}}.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("invalid bounded sandbox file request")
	}
	req.Header.Set("X-Access-Token", s.sb.EnvdAccessToken)
	req.Header.Set("cube-traffic-access-token", s.sb.TrafficAccessToken)
	req.Header.Set("e2b-traffic-access-token", s.sb.TrafficAccessToken)
	resp, err := s.fileHTTP.Do(req)
	if err != nil {
		return nil, errors.New("bounded sandbox file transfer failed")
	}
	defer resp.Body.Close()
	// Never read an error body: it is also sandbox-controlled and may be huge.
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("bounded sandbox file unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("bounded sandbox file transfer failed")
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("sandbox file exceeds transfer limit")
	}
	return data, nil
}

var _ sandbox.BoundedFileReader = (*cubeSandbox)(nil)
