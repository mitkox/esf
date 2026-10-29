package cube

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

type boundedFileRoundTrip func(*http.Request) (*http.Response, error)

func (f boundedFileRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type generatedFileBody struct {
	remaining int64
	read      int64
	closed    bool
}

func (b *generatedFileBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > b.remaining {
		n = b.remaining
	}
	clear(p[:int(n)])
	b.remaining -= n
	b.read += n
	return int(n), nil
}
func (b *generatedFileBody) Close() error { b.closed = true; return nil }

func TestReadFileBoundedCapsStreamRegardlessOfAdvertisedSize(t *testing.T) {
	for _, size := range []int64{31, 32, 33, 1 << 40} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			body := &generatedFileBody{remaining: size}
			client := &http.Client{Transport: boundedFileRoundTrip(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/files" || req.URL.Query().Get("path") != "/tmp/private source.tar" || req.URL.Host != "49983-owned.preview.test" {
					t.Fatal("incorrect envd file routing")
				}
				if req.Header.Get("X-Access-Token") != "private-envd" || req.Header.Get("cube-traffic-access-token") != "private-traffic" || req.Header.Get("e2b-traffic-access-token") != "private-traffic" {
					t.Fatal("missing data-plane authentication")
				}
				return &http.Response{StatusCode: 200, Body: body, ContentLength: 0, Header: make(http.Header)}, nil
			})}
			sb := &cubeSandbox{sb: &cubesandbox.Sandbox{SandboxID: "owned", Domain: "preview.test", EnvdAccessToken: "private-envd", TrafficAccessToken: "private-traffic"}, fileHTTP: client, fileScheme: "https"}
			data, err := sb.ReadFileBounded(context.Background(), "/tmp/private source.tar", 32)
			if size <= 32 && (err != nil || int64(len(data)) != size) {
				t.Fatalf("bounded file rejected: %v", err)
			}
			if size > 32 && (err == nil || data != nil) {
				t.Fatal("oversized response returned bytes")
			}
			if body.read > 33 || !body.closed {
				t.Fatalf("unbounded or unclosed stream: read=%d closed=%v", body.read, body.closed)
			}
		})
	}
}

func TestReadFileBoundedRejectsRedirectAndErrorWithoutReadingBody(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusInternalServerError} {
		client, scheme := boundedFileHTTPClient(cubesandbox.Config{ProxyScheme: "https"})
		calls := 0
		body := &generatedFileBody{remaining: 1 << 40}
		client.Transport = boundedFileRoundTrip(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Body: body, Header: http.Header{"Location": []string{"https://attacker.invalid/files"}}}, nil
		})
		sb := &cubeSandbox{sb: &cubesandbox.Sandbox{SandboxID: "owned", Domain: "preview.test"}, fileHTTP: client, fileScheme: scheme}
		if data, err := sb.ReadFileBounded(context.Background(), "/tmp/private", 32); err == nil || data != nil {
			t.Fatal("accepted redirect or error")
		}
		if calls != 1 || body.read != 0 || !body.closed {
			t.Fatalf("followed redirect or read untrusted error body: calls=%d bytes=%d", calls, body.read)
		}
	}
}

func TestReadFileBoundedRejectsInvalidBoundsWithoutTransport(t *testing.T) {
	sb := &cubeSandbox{}
	for _, limit := range []int64{-1, 0, 1<<63 - 1} {
		if _, err := sb.ReadFileBounded(context.Background(), "/tmp/file", limit); err == nil {
			t.Fatal("accepted invalid limit")
		}
	}
	client, _ := boundedFileHTTPClient(cubesandbox.Config{})
	if client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirects are enabled")
	}
	if transport, ok := client.Transport.(*http.Transport); !ok || transport.Proxy != nil || !transport.DisableCompression {
		t.Fatal("bounded transfer permits ambient proxy or automatic decompression")
	}
	client.CloseIdleConnections()
}

func TestReadFileBoundedDialsConfiguredProxyWithVirtualHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != "49983-owned.unresolvable.invalid" {
			t.Errorf("data-plane virtual host = %q", req.Host)
		}
		_, _ = w.Write([]byte("file"))
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	client, scheme := boundedFileHTTPClient(cubesandbox.Config{ProxyNodeIP: host, ProxyPortHTTP: port, ProxyScheme: "http"})
	defer client.CloseIdleConnections()
	sb := &cubeSandbox{sb: &cubesandbox.Sandbox{SandboxID: "owned", Domain: "unresolvable.invalid"}, fileHTTP: client, fileScheme: scheme}
	data, err := sb.ReadFileBounded(context.Background(), "/tmp/file", 4)
	if err != nil || string(data) != "file" {
		t.Fatalf("configured proxy routing failed: %v", err)
	}
}
