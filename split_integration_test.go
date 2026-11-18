package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// split_integration_test.go — end-to-end test of split-tunnel routing.
//
// The combined SOCKS5 listener is started with split tunneling enabled
// and a domain pattern list.  An HTTP client connects via SOCKS5 to an
// IP target (127.0.0.1:port) but sets a custom Host header.  The
// combined handler peeks at the first packet, extracts the Host
// header, and routes through the backend proxy only if the domain
// matches.  A recording SOCKS5 backend counts how many times it is
// contacted so the test can verify the routing decision.

// startRecordingSOCKS5Backend starts a SOCKS5 proxy that increments
// hits for each connection it handles.  Returns its address.
func startRecordingSOCKS5Backend(t *testing.T, hits *int32) string {
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
			atomic.AddInt32(hits, 1)
			go handleSOCKS5Backend(c)
		}
	}()
	return ln.Addr().String()
}

// fetchWithHost makes an HTTP GET through the combined SOCKS5 proxy to
// targetIP (an IP:port), but sets the HTTP Host header to hostHeader.
// This simulates a client that resolved the domain locally and is
// connecting by IP — forcing the combined handler to peek at the HTTP
// Host header to recover the domain for the split-tunnel decision.
func fetchWithHost(t *testing.T, targetIP, hostHeader string) (string, error) {
	dialer, err := proxy.SOCKS5("tcp",
		fmt.Sprintf("127.0.0.1:%d", combinedPort), nil,
		&net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			// Ignore the addr from the URL; dial targetIP through SOCKS5.
			Dial: func(network, addr string) (net.Conn, error) {
				return dialer.Dial("tcp", targetIP)
			},
		},
		Timeout: 10 * time.Second,
	}
	req, err := http.NewRequest("GET", "http://"+hostHeader+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = hostHeader
	resp, err := client.Do(req)
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

// TestSplitTunnelRouting verifies that:
//   - A domain in the pattern list is routed through the backend proxy.
//   - A domain NOT in the list gets a direct connection (backend not
//     contacted).
//   - With split disabled, everything goes through the proxy.
func TestSplitTunnelRouting(t *testing.T) {
	target := startTarget(t)

	// Recording SOCKS5 backend.
	var hits int32
	backend := startRecordingSOCKS5Backend(t, &hits)

	// --- Split enabled: *.proxied.com matches, *.direct.com does not ---
	cm := startCombinedListener(t, backend, "socks5")
	cm.SetSplit(true, []string{"*.proxied.com"})
	defer cm.Stop()

	// Matching domain → should go through the proxy (hits increments).
	atomic.StoreInt32(&hits, 0)
	body, err := fetchWithHost(t, target, "www.proxied.com")
	if err != nil {
		t.Fatalf("matching domain request failed: %v", err)
	}
	if body != "TARGET-OK" {
		t.Fatalf("matching domain: unexpected body %q", body)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("matching domain should route through proxy; backend hits = %d, want 1", got)
	} else {
		t.Logf("matching domain www.proxied.com → proxied (backend hit)")
	}

	// Non-matching domain → should go direct (hits stays 0).
	atomic.StoreInt32(&hits, 0)
	body, err = fetchWithHost(t, target, "www.direct.com")
	if err != nil {
		t.Fatalf("non-matching domain request failed: %v", err)
	}
	if body != "TARGET-OK" {
		t.Fatalf("non-matching domain: unexpected body %q", body)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("non-matching domain should go direct; backend hits = %d, want 0", got)
	} else {
		t.Logf("non-matching domain www.direct.com → direct (no backend hit)")
	}

	// --- Split disabled: everything goes through the proxy ---
	cm.SetSplit(false, nil)

	atomic.StoreInt32(&hits, 0)
	body, err = fetchWithHost(t, target, "www.direct.com")
	if err != nil {
		t.Fatalf("split-disabled request failed: %v", err)
	}
	if body != "TARGET-OK" {
		t.Fatalf("split-disabled: unexpected body %q", body)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("split disabled should always proxy; backend hits = %d, want 1", got)
	} else {
		t.Logf("split disabled www.direct.com → proxied (backend hit)")
	}
}

// TestSplitTunnelDomainTarget tests the domain-target path: when the
// SOCKS5 client sends a domain name (not an IP), the handler decides
// directly without peeking.  We can't actually connect to a fake
// domain, but we can verify the backend is contacted for matching
// domains by having the backend proxy simulate a successful dial.
//
// Actually, the backend proxy tries to dial the real domain and fails.
// So instead, we test the IP-target peek path (above) which is the
// path that exercises SNI/Host extraction.  The domain-target path is
// covered by the unit tests (TestShouldProxy, TestDomainMatcher).
