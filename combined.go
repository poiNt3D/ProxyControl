package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// combinedPort is the port the Combined (Общий) mode listens on.
// Applications point their SOCKS5 proxy at 127.0.0.1:8880; the combined
// listener chains each connection to whichever backend proxy is
// currently selected (WireProxy, ByeDPI, or NoDPI).
const combinedPort = 8880

// CombinedMode manages a SOCKS5 listener on combinedPort that chains
// connections to a selected backend proxy.  It gives applications a
// single, protocol-consistent entry point: regardless of whether the
// backend is a SOCKS5 proxy (WireProxy, ByeDPI) or an HTTP proxy
// (NoDPI), clients always speak SOCKS5 to :8880.  For an HTTP backend
// each SOCKS5 CONNECT is translated into an HTTP CONNECT so the
// backend's protocol is transparent to the client.
type CombinedMode struct {
	mu       sync.Mutex
	backend  string // "off", "wireproxy", "byedpi", "nodpi"
	port     int    // backend port (0 when off)
	ptype    string // "socks5" or "http"
	listener net.Listener

	splitEnabled bool           // split-tunnel on/off
	matcher      *domainMatcher // compiled domain patterns (nil when off)
}

// SetBackend selects the backend proxy to chain to.  If key is "off" the
// listener is stopped.  Otherwise the named service is started (if not
// already running) and a SOCKS5 listener is (re)started on
// combinedPort.  Any previously running listener is closed first.
func (c *CombinedMode) SetBackend(services map[string]*ServiceDef, key string) error {
	// Stop the current listener.
	c.mu.Lock()
	if c.listener != nil {
		c.listener.Close()
		c.listener = nil
	}
	c.backend = "off"
	c.port = 0
	c.ptype = ""
	c.mu.Unlock()

	if key == "off" || key == "" {
		return nil
	}

	svc := services[key]
	if svc == nil {
		return fmt.Errorf(T("err.unknown_service"), key)
	}

	// Start the backend service if it is not already running.
	if !svc.IsActive() {
		if !svc.HasActiveConfig() {
			svc.EnsureDefaultConfig()
		}
		if err := svc.Start(); err != nil {
			return fmt.Errorf(T("err.cannot_start"), svc.Label, err)
		}
		// Give the backend a moment to bind its port before we chain to it.
		time.Sleep(500 * time.Millisecond)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", combinedPort))
	if err != nil {
		return fmt.Errorf(T("err.cannot_listen"), combinedPort, err)
	}

	c.mu.Lock()
	c.backend = key
	c.port = svc.Port()
	c.ptype = svc.ProxyType()
	c.listener = ln
	c.mu.Unlock()

	go c.acceptLoop(ln)
	return nil
}

// Backend returns the currently selected backend key ("off",
// "wireproxy", "byedpi", or "nodpi").
func (c *CombinedMode) Backend() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.backend
}

// IsActive reports whether the combined listener is currently running.
func (c *CombinedMode) IsActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listener != nil
}

// Stop closes the listener and resets the selection.  Used on
// application exit.
func (c *CombinedMode) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.listener != nil {
		c.listener.Close()
		c.listener = nil
	}
	c.backend = "off"
	c.port = 0
	c.ptype = ""
}

// SetSplit enables or disables split tunneling and updates the
// domain pattern list.  When enabled, the combined listener peeks at
// each connection's domain (from the SOCKS5 target, or from the TLS
// SNI / HTTP Host when the target is an IP) and routes through the
// backend proxy only if the domain matches a pattern; everything else
// gets a direct connection.  Safe to call while the listener is
// running — the next accepted connection picks up the new settings.
func (c *CombinedMode) SetSplit(enabled bool, domains []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.splitEnabled = enabled
	if enabled {
		c.matcher = newDomainMatcher(domains)
	} else {
		c.matcher = nil
	}
}

// SplitEnabled reports whether split tunneling is currently on.
func (c *CombinedMode) SplitEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.splitEnabled
}

// SplitDomainCount returns the number of compiled domain patterns, or
// 0 if split tunneling is disabled.
func (c *CombinedMode) SplitDomainCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.matcher.count()
}

// acceptLoop accepts connections on ln until it is closed.  Each
// listener gets its own loop so a SetBackend restart (which closes the
// old listener) cleanly terminates the old loop.
func (c *CombinedMode) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go c.handle(conn)
	}
}

// handle services one SOCKS5 client connection: handshake, decide
// whether to route through the backend proxy or directly (split
// tunneling), dial the target, reply, then relay.
//
// Split-tunnel decision:
//   - Split disabled → always use the backend proxy.
//   - Split enabled, SOCKS5 target is a domain → check the domain
//     against the pattern list; match = proxy, no match = direct.
//   - Split enabled, SOCKS5 target is an IP → peek at the first
//     application packet (TLS ClientHello SNI or HTTP Host header)
//     to recover the domain, then check the list.  If no domain can
//     be extracted, fall back to direct.
func (c *CombinedMode) handle(client net.Conn) {
	defer client.Close()
	// Give the handshake a deadline so a half-open client cannot hold a
	// goroutine forever.
	client.SetDeadline(time.Now().Add(30 * time.Second))

	c.mu.Lock()
	port := c.port
	ptype := c.ptype
	backend := c.backend
	splitEnabled := c.splitEnabled
	matcher := c.matcher
	c.mu.Unlock()

	if backend == "off" || port == 0 {
		return
	}

	// bufio.Reader so we can drain any bytes the client pipelined after
	// the SOCKS5 request without losing them.
	br := bufio.NewReader(client)
	target, err := socks5Handshake(br, client)
	if err != nil {
		return
	}

	host, _, _ := net.SplitHostPort(target)
	isIP := net.ParseIP(host) != nil

	// Determine the routing decision and any first-packet data we need
	// to forward to the remote.
	var firstPacket []byte
	replied := false // true once we've sent the SOCKS5 success reply
	useProxy := true

	switch {
	case !splitEnabled:
		// Everything goes through the proxy.

	case !isIP:
		// Domain target — decide directly from the hostname.
		useProxy = matcher.match(host)

	default:
		// IP target — peek at the first application packet to recover
		// the domain via TLS SNI or HTTP Host header.
		if br.Buffered() > 0 {
			firstPacket = make([]byte, br.Buffered())
			_, _ = io.ReadFull(br, firstPacket)
		}
		// If the client did not pipeline, we must send the success
		// reply so it starts sending data, then read the first packet.
		if len(firstPacket) == 0 {
			if err := socks5Reply(client, 0x00); err != nil {
				return
			}
			replied = true
			client.SetDeadline(time.Now().Add(10 * time.Second))
			buf := make([]byte, 4096)
			n, rerr := br.Read(buf)
			if rerr != nil || n == 0 {
				return // client sent nothing
			}
			firstPacket = buf[:n]
		}
		client.SetDeadline(time.Time{})

		domain := extractDomain(firstPacket)
		if domain != "" {
			useProxy = matcher.match(domain)
		} else {
			// No domain recoverable — go direct.
			useProxy = false
		}
	}

	// Connect to the target — through the backend proxy or directly.
	backendAddr := fmt.Sprintf("127.0.0.1:%d", port)
	var remote net.Conn
	if useProxy {
		if ptype == "http" {
			remote, err = dialHTTPProxy(backendAddr, target)
		} else {
			remote, err = dialSOCKS5(backendAddr, target)
		}
	} else {
		remote, err = net.DialTimeout("tcp", target, 10*time.Second)
	}
	if err != nil {
		if !replied {
			_ = socks5Reply(client, 0x01) // general SOCKS server failure
		}
		return
	}
	defer remote.Close()

	// Send the success reply unless the peek path already sent it.
	if !replied {
		if err := socks5Reply(client, 0x00); err != nil {
			return
		}
	}

	// Forward any first packet we read early (SNI peek) to the remote.
	if len(firstPacket) > 0 {
		if _, err := remote.Write(firstPacket); err != nil {
			return
		}
	}

	// If the bufio.Reader still has buffered bytes (client pipelined
	// after the first packet), wrap the conn so they are seen first.
	var conn net.Conn = client
	if br.Buffered() > 0 {
		buf := make([]byte, br.Buffered())
		_, _ = io.ReadFull(br, buf)
		conn = &bufferedConn{
			r:    io.MultiReader(bytes.NewReader(buf), client),
			Conn: client,
		}
	}

	relay(conn, remote)
}

// --- SOCKS5 server-side handshake ----------------------------------------

// socks5Handshake reads the SOCKS5 greeting and CONNECT request from br
// and writes the greeting reply to c.  It returns the target address
// as "host:port" (host may be an IP literal or a domain name — domain
// names are forwarded verbatim so the backend proxy does DNS
// resolution).  Only the no-authentication method and the CONNECT
// command are supported.
func socks5Handshake(br *bufio.Reader, c net.Conn) (string, error) {
	// Greeting: [VER, NMETHODS, METHODS...]
	ver, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	if ver != 0x05 {
		return "", fmt.Errorf("not socks5: version %d", ver)
	}
	nmethods, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	if nmethods == 0 {
		return "", fmt.Errorf("no socks5 methods offered")
	}
	if _, err := io.ReadFull(br, make([]byte, int(nmethods))); err != nil {
		return "", err
	}
	// Select no-authentication.
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return "", err
	}

	// Request: [VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT]
	header := make([]byte, 4)
	if _, err := io.ReadFull(br, header); err != nil {
		return "", err
	}
	if header[0] != 0x05 {
		return "", fmt.Errorf("bad request version %d", header[0])
	}
	if header[1] != 0x01 { // only CONNECT
		_ = socks5Reply(c, 0x07) // command not supported
		return "", fmt.Errorf("unsupported command %d", header[1])
	}
	atyp := header[3]

	var host string
	switch atyp {
	case 0x01: // IPv4
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	case 0x03: // domain name
		lenByte, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		buf := make([]byte, int(lenByte))
		if _, err := io.ReadFull(br, buf); err != nil {
			return "", err
		}
		host = string(buf)
	case 0x04: // IPv6
		buf := make([]byte, 16)
		if _, err := io.ReadFull(br, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	default:
		_ = socks5Reply(c, 0x08) // address type not supported
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}

	// Port: 2 bytes, big-endian.
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(br, portBuf); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBuf)

	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// socks5Reply writes a SOCKS5 reply with the given reply code.  The
// bound address/port are zeroed (clients do not require them).
func socks5Reply(c net.Conn, rep byte) error {
	_, err := c.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

// --- backend dialers ------------------------------------------------------

// dialSOCKS5 connects to target through a SOCKS5 proxy at proxyAddr.
func dialSOCKS5(proxyAddr, target string) (net.Conn, error) {
	d, err := proxy.SOCKS5("tcp", proxyAddr, nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	return d.Dial("tcp", target)
}

// dialHTTPProxy connects to target by issuing an HTTP CONNECT to the
// HTTP proxy at proxyAddr (used for the NoDPI backend).  Any bytes the
// proxy sends after the 200 response headers are preserved by wrapping
// the connection.
func dialHTTPProxy(proxyAddr, target string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", proxyAddr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		c.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.Close()
		return nil, fmt.Errorf(T("err.http_proxy_reject"), proxyAddr, resp.Status)
	}
	// Preserve any bytes the proxy sent right after the headers.
	if br.Buffered() > 0 {
		buf := make([]byte, br.Buffered())
		_, _ = io.ReadFull(br, buf)
		return &bufferedConn{
			r:    io.MultiReader(bytes.NewReader(buf), c),
			Conn: c,
		}, nil
	}
	return c, nil
}

// --- helpers --------------------------------------------------------------

// relay copies data bidirectionally between two connections.  When
// either side closes, both are closed and the goroutines are reaped.
func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}

// bufferedConn wraps a net.Conn so reads first drain a prefixed byte
// slice (data a bufio.Reader had buffered) before reading from the
// underlying connection.
type bufferedConn struct {
	r io.Reader
	net.Conn
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }
