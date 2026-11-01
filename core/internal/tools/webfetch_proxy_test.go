package tools

import (
	"bufio"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeProxy is an HTTP proxy that accepts CONNECT for any target and
// tunnels it to origin, recording what it was asked for.
type fakeProxy struct {
	url     *url.URL
	mu      sync.Mutex
	targets []string
	auth    []string
}

func startFakeProxy(t *testing.T, origin string) *fakeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	p := &fakeProxy{url: &url.URL{Scheme: "http", Host: ln.Addr().String()}}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(conn, origin)
		}
	}()
	return p
}

func (p *fakeProxy) serve(conn net.Conn, origin string) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.targets = append(p.targets, req.Method+" "+req.Host)
	p.auth = append(p.auth, req.Header.Get("Proxy-Authorization"))
	p.mu.Unlock()
	if req.Method != http.MethodConnect {
		fmt.Fprint(conn, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	up, err := net.Dial("tcp", origin)
	if err != nil {
		fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer up.Close()
	fmt.Fprint(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go func() { io.Copy(up, br); up.Close() }()
	io.Copy(conn, up)
}

func (p *fakeProxy) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

func withProxy(t *testing.T, u *url.URL) {
	t.Helper()
	old := webProxy
	webProxy = func(*http.Request) (*url.URL, error) { return u, nil }
	t.Cleanup(func() { webProxy = old })
}

func originPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// Through a proxy, the name is still resolved and checked here, and the
// proxy is asked for the checked address, so it cannot pick another.
func TestWebFetchThroughAProxyPinsTheCheckedAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "http://sub.example.com:"+r.Host[strings.LastIndex(r.Host, ":")+1:]+"/", http.StatusFound)
			return
		}
		fmt.Fprintf(w, "hello from %s", r.Host)
	}))
	defer srv.Close()
	port := originPort(t, srv)
	proxy := startFakeProxy(t, srv.Listener.Addr().String())
	withProxy(t, proxy.url)
	fakeDNS(t, map[string]string{"example.com": "93.184.216.34", "lan.example.com": "10.0.0.7", "sub.example.com": "192.168.4.4"})

	resp, err := fetch(t, nil, map[string]any{"url": "http://example.com:" + port + "/"})
	if err != nil || resp.Content != "hello from example.com:"+port {
		t.Fatalf("public name: %v %+v", err, resp)
	}
	if got := proxy.seen(); len(got) != 1 || got[0] != "CONNECT 93.184.216.34:"+port {
		t.Fatalf("the proxy must be asked for the checked address: %v", got)
	}

	for _, c := range []struct{ url, want string }{
		// Resolves here to a private address: refused before the proxy.
		{"http://lan.example.com:" + port + "/", "private network"},
		// Only the proxy could resolve it: nothing to check, so refused.
		{"http://build.corp.internal:" + port + "/", "does not resolve on this machine"},
		// A same-site redirect is checked like the first request.
		{"http://example.com:" + port + "/hop", "private network"},
	} {
		if _, err := fetch(t, nil, map[string]any{"url": c.url}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.url, c.want, err)
		}
	}
	if got := proxy.seen(); len(got) != 2 {
		t.Fatalf("refused targets reached the proxy: %v", got)
	}
}

// With private networks allowed, a name only the proxy knows (an
// intranet host) goes to the proxy by name.
func TestWebFetchLetsTheProxyResolveWhenPrivateIsAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "intranet page") }))
	defer srv.Close()
	port := originPort(t, srv)
	proxy := startFakeProxy(t, srv.Listener.Addr().String())
	u := *proxy.url
	u.User = url.UserPassword("ada", "s3cret")
	withProxy(t, &u)
	fakeDNS(t, map[string]string{})
	resp, err := fetch(t, &ToolCtx{Web: WebOptions{AllowPrivate: true}}, map[string]any{"url": "http://build.corp.internal:" + port + "/"})
	if err != nil || resp.Content != "intranet page" {
		t.Fatalf("opt-in: %v %+v", err, resp)
	}
	if got := proxy.seen(); len(got) != 1 || got[0] != "CONNECT build.corp.internal:"+port {
		t.Fatalf("targets %v", got)
	}
	if proxy.auth[0] != "Basic YWRhOnMzY3JldA==" {
		t.Fatalf("proxy credentials: %q", proxy.auth[0])
	}
}

// https through the proxy: the tunnel goes to the checked address and
// TLS still verifies the certificate for the name.
func TestWebFetchHTTPSThroughAProxy(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "secure page") }))
	defer srv.Close()
	port := originPort(t, srv)
	proxy := startFakeProxy(t, srv.Listener.Addr().String())
	withProxy(t, proxy.url)
	fakeDNS(t, map[string]string{"example.com": "93.184.216.34", "other.test": "93.184.216.35"})
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	old := webRootCAs
	webRootCAs = pool
	t.Cleanup(func() { webRootCAs = old })

	resp, err := fetch(t, nil, map[string]any{"url": "https://example.com:" + port + "/"})
	if err != nil || resp.Content != "secure page" {
		t.Fatalf("https: %v %+v", err, resp)
	}
	if got := proxy.seen(); got[0] != "CONNECT 93.184.216.34:"+port {
		t.Fatalf("targets %v", got)
	}
	// The certificate is for example.com, not other.test.
	if _, err := fetch(t, nil, map[string]any{"url": "https://other.test:" + port + "/"}); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("certificate check: %v", err)
	}
}
