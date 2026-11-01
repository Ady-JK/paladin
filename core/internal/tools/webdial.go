// webdial.go opens web_fetch's connections. Every name is resolved and
// checked here (checkHost) and the connection goes to a checked address:
// directly, or through the user's proxy with a CONNECT to that address,
// so a proxy cannot resolve the name to somewhere else. A name that does
// not resolve here cannot be checked; it goes to the proxy by name only
// when the user allows private networks, which leaves nothing to check.

package tools

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// webRootCAs replaces the system roots; tests set it.
var webRootCAs *x509.CertPool

// resolveError is a name that did not resolve on this machine.
type resolveError struct {
	host string
	err  error
}

func (e *resolveError) Error() string {
	if e.err == nil {
		return "cannot resolve " + e.host
	}
	return fmt.Sprintf("cannot resolve %s: %v", e.host, e.err)
}

func (e *resolveError) Unwrap() error { return e.err }

type webDialer struct {
	opts WebOptions
	net  net.Dialer
}

func newWebDialer(opts WebOptions) *webDialer {
	return &webDialer{opts: opts, net: net.Dialer{Timeout: 10 * time.Second}}
}

// dialPlain is the Transport's DialContext (http:// URLs).
func (d *webDialer) dialPlain(ctx context.Context, _, addr string) (net.Conn, error) {
	return d.dial(ctx, "http", addr)
}

// dialTLS is the Transport's DialTLSContext (https:// URLs): the same
// connection, then TLS verified for the name.
func (d *webDialer) dialTLS(ctx context.Context, _, addr string) (net.Conn, error) {
	conn, err := d.dial(ctx, "https", addr)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	tc := tls.Client(conn, &tls.Config{ServerName: host, RootCAs: webRootCAs, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12})
	if err := tc.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return tc, nil
}

func (d *webDialer) dial(ctx context.Context, scheme, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	proxy, err := webProxy(&http.Request{URL: &url.URL{Scheme: scheme, Host: addr}})
	if err != nil {
		return nil, fmt.Errorf("proxy settings: %w", err)
	}
	ips, err := checkHost(ctx, host, d.opts.AllowPrivate)
	var unresolved *resolveError
	if err != nil && proxy != nil && errors.As(err, &unresolved) {
		if d.opts.AllowPrivate {
			return d.tunnel(ctx, proxy, addr)
		}
		return nil, fmt.Errorf("cannot check where %s leads: it does not resolve on this machine, and the proxy would choose the address. "+
			"Turn on Private and internal networks in Settings → Tools to let the proxy resolve it", host)
	}
	if err != nil {
		return nil, err
	}
	var last error
	for _, ip := range ips {
		target := net.JoinHostPort(ip.String(), port)
		var conn net.Conn
		if proxy != nil {
			conn, err = d.tunnel(ctx, proxy, target)
		} else {
			conn, err = d.net.DialContext(ctx, "tcp", target)
		}
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

// tunnel asks proxy for a connection to target (host:port).
func (d *webDialer) tunnel(ctx context.Context, proxy *url.URL, target string) (net.Conn, error) {
	switch proxy.Scheme {
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if proxy.User != nil {
			pw, _ := proxy.User.Password()
			auth = &xproxy.Auth{User: proxy.User.Username(), Password: pw}
		}
		socks, err := xproxy.SOCKS5("tcp", proxyAddr(proxy), auth, &d.net)
		if err != nil {
			return nil, err
		}
		return socks.(xproxy.ContextDialer).DialContext(ctx, "tcp", target)
	case "http", "https", "":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", proxy.Scheme)
	}
	conn, err := d.net.DialContext(ctx, "tcp", proxyAddr(proxy))
	if err != nil {
		return nil, fmt.Errorf("proxy: %w", err)
	}
	if proxy.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: proxy.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("proxy: %w", err)
		}
		conn = tc
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if proxy.User != nil {
		pw, _ := proxy.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+pw)))
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("the proxy refused a connection to %s: %s", target, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

func proxyAddr(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	switch u.Scheme {
	case "https":
		port = "443"
	case "socks5", "socks5h":
		port = "1080"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// bufferedConn reads what the proxy sent after its answer first.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
