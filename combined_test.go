package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// These tests exercise the combined-mode SOCKS5 server end-to-end:
// a real SOCKS5 client (via golang.org/x/net/proxy) connects to the
// CombinedMode listener on combinedPort, which chains to a fake
// backend proxy (HTTP or SOCKS5), which relays to a target HTTP
// server.  Both the HTTP-backend path (dialHTTPProxy + CONNECT
// translation) and the SOCKS5-backend path (dialSOCKS5) are covered.

// startTarget starts an HTTP server returning a known body.
func startTarget(t *testing.T) string {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "TARGET-OK")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go (&http.Server{Handler: mux}).Serve(ln)
	return ln.Addr().String()
}

// startHTTPProxyBackend starts a minimal HTTP CONNECT proxy and returns
// its address.  This stands in for NoDPI.
func startHTTPProxyBackend(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleHTTPProxyBackend(c)
		}
	}()
	return ln.Addr().String()
}

func handleHTTPProxyBackend(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil || req.Method != "CONNECT" {
		return
	}
	remote, err := net.DialTimeout("tcp", req.URL.Host, 5*time.Second)
	if err != nil {
		c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer remote.Close()
	c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	var rc net.Conn = c
	if br.Buffered() > 0 {
		buf := make([]byte, br.Buffered())
		_, _ = io.ReadFull(br, buf)
		rc = &bufferedConn{r: io.MultiReader(bytes.NewReader(buf), c), Conn: c}
	}
	relay(rc, remote)
}

// startSOCKS5Backend starts a minimal SOCKS5 proxy and returns its
// address.  This stands in for WireProxy / ByeDPI.
func startSOCKS5Backend(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5Backend(c)
		}
	}()
	return ln.Addr().String()
}

func handleSOCKS5Backend(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	if v, _ := br.ReadByte(); v != 5 {
		return
	}
	nm, _ := br.ReadByte()
	_, _ = io.ReadFull(br, make([]byte, int(nm)))
	_, _ = c.Write([]byte{5, 0})
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	if hdr[1] != 1 {
		_, _ = c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch hdr[3] {
	case 1:
		b := make([]byte, 4)
		_, _ = io.ReadFull(br, b)
		host = net.IP(b).String()
	case 3:
		l, _ := br.ReadByte()
		b := make([]byte, int(l))
		_, _ = io.ReadFull(br, b)
		host = string(b)
	default:
		return
	}
	pb := make([]byte, 2)
	_, _ = io.ReadFull(br, pb)
	port := binary.BigEndian.Uint16(pb)
	target := fmt.Sprintf("%s:%d", host, port)
	var rc net.Conn = c
	if br.Buffered() > 0 {
		buf := make([]byte, br.Buffered())
		_, _ = io.ReadFull(br, buf)
		rc = &bufferedConn{r: io.MultiReader(bytes.NewReader(buf), c), Conn: c}
	}
	remote, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	relay(rc, remote)
}

// startCombinedListener creates a CombinedMode listening on combinedPort
// with the given backend address and proxy type.
func startCombinedListener(t *testing.T, backendAddr, ptype string) *CombinedMode {
	_, portStr, _ := net.SplitHostPort(backendAddr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", combinedPort))
	if err != nil {
		t.Fatal(err)
	}
	cm := &CombinedMode{}
	cm.mu.Lock()
	cm.backend = "test"
	cm.port = port
	cm.ptype = ptype
	cm.listener = ln
	cm.mu.Unlock()
	go cm.acceptLoop(ln)
	return cm
}

// fetchThroughCombined does an HTTP GET through the combined SOCKS5 proxy.
func fetchThroughCombined(t *testing.T, targetAddr string) (string, error) {
	dialer, err := proxy.SOCKS5("tcp",
		fmt.Sprintf("127.0.0.1:%d", combinedPort), nil,
		&net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{Dial: dialer.Dial},
		Timeout:   10 * time.Second,
	}
	resp, err := client.Get("http://" + targetAddr + "/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func TestCombinedChaining(t *testing.T) {
	target := startTarget(t)

	// --- HTTP backend (NoDPI-style) ---
	httpBackend := startHTTPProxyBackend(t)
	cm1 := startCombinedListener(t, httpBackend, "http")
	body, err := fetchThroughCombined(t, target)
	cm1.Stop()
	if err != nil {
		t.Fatalf("HTTP backend chain failed: %v", err)
	}
	if body != "TARGET-OK" {
		t.Fatalf("HTTP backend: unexpected body %q", body)
	}
	t.Logf("HTTP backend chain OK: client → :%d → %s → %s",
		combinedPort, httpBackend, target)

	// --- SOCKS5 backend (WireProxy / ByeDPI-style) ---
	socksBackend := startSOCKS5Backend(t)
	cm2 := startCombinedListener(t, socksBackend, "socks5")
	body, err = fetchThroughCombined(t, target)
	cm2.Stop()
	if err != nil {
		t.Fatalf("SOCKS5 backend chain failed: %v", err)
	}
	if body != "TARGET-OK" {
		t.Fatalf("SOCKS5 backend: unexpected body %q", body)
	}
	t.Logf("SOCKS5 backend chain OK: client → :%d → %s → %s",
		combinedPort, socksBackend, target)
}
