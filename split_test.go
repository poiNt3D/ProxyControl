package main

import (
	"testing"
)

// buildClientHello constructs a minimal TLS 1.2 ClientHello record
// carrying the given SNI hostname.  Used to test extractSNI without
// making real network connections.
func buildClientHello(sni string) []byte {
	// SNI extension: type(0x0000) + length + server_name list.
	name := []byte(sni)
	entry := []byte{0x00, byte(len(name) >> 8), byte(len(name))}
	entry = append(entry, name...)
	list := []byte{byte(len(entry) >> 8), byte(len(entry))}
	list = append(list, entry...)
	sniExt := []byte{0x00, 0x00, byte(len(list) >> 8), byte(len(list))}
	sniExt = append(sniExt, list...)

	// ClientHello body.
	body := []byte{0x03, 0x03}                  // version TLS 1.2
	body = append(body, make([]byte, 32)...)   // random
	body = append(body, 0x00)                  // session ID length = 0
	body = append(body, 0x00, 0x02, 0xc0, 0x2c) // 1 cipher suite
	body = append(body, 0x01, 0x00)            // 1 compression method (null)
	body = append(body, byte(len(sniExt)>>8), byte(len(sniExt)))
	body = append(body, sniExt...)

	// Handshake header.
	hsLen := len(body)
	hs := []byte{0x01, byte(hsLen >> 16), byte(hsLen >> 8), byte(hsLen)}
	hs = append(hs, body...)

	// TLS record header.
	recLen := len(hs)
	rec := []byte{0x16, 0x03, 0x01, byte(recLen >> 8), byte(recLen)}
	rec = append(rec, hs...)
	return rec
}

// --- wildcard matching ----------------------------------------------------

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		pattern, domain string
		want            bool
	}{
		// exact
		{"google.com", "google.com", true},
		{"google.com", "www.google.com", false},
		{"google.com", "Google.COM", true}, // case-insensitive
		{"google.com", "evilgoogle.com", false},

		// *.example.com — subdomains only
		{"*.google.com", "www.google.com", true},
		{"*.google.com", "mail.google.com", true},
		{"*.google.com", "google.com", false}, // apex does not match
		{"*.google.com", "a.b.google.com", true},

		// *example.com — anything ending in example.com
		{"*google.com", "google.com", true},
		{"*google.com", "www.google.com", true},
		{"*google.com", "evilgoogle.com", true},

		// bare *
		{"*", "anything.com", true},
		{"*", "google.com", true},

		// edge cases
		{"", "google.com", false},
		{"google.com", "", false},
		{"  google.com  ", "google.com", true}, // trimmed
	}
	for i, tc := range cases {
		got := matchDomain(tc.pattern, tc.domain)
		if got != tc.want {
			t.Errorf("case %d: matchDomain(%q, %q) = %v, want %v",
				i, tc.pattern, tc.domain, got, tc.want)
		}
	}
}

func TestDomainInList(t *testing.T) {
	list := []string{"*.google.com", "*.youtube.com", "github.com", "*"}
	// * matches everything, so everything is true — test without *
	list2 := []string{"*.google.com", "*.youtube.com", "github.com"}
	cases := []struct {
		domain string
		list   []string
		want   bool
	}{
		{"www.google.com", list2, true},
		{"mail.google.com", list2, true},
		{"google.com", list2, false}, // apex not matched by *.google.com
		{"www.youtube.com", list2, true},
		{"github.com", list2, true},
		{"www.github.com", list2, false}, // exact match only
		{"random.com", list2, false},
		{"anything.com", list, true}, // * matches
	}
	for _, tc := range cases {
		got := domainInList(tc.domain, tc.list)
		if got != tc.want {
			t.Errorf("domainInList(%q, %v) = %v, want %v",
				tc.domain, tc.list, got, tc.want)
		}
	}
}

// --- compiled matcher -----------------------------------------------------

func TestDomainMatcher(t *testing.T) {
	m := newDomainMatcher([]string{
		"*.google.com",
		"github.com",
		"*youtube.com",
		"# this is a comment",
		"",
		"   ",
	})
	if m.count() != 3 {
		t.Errorf("matcher count = %d, want 3 (comment+blank skipped)", m.count())
	}
	if !m.match("www.google.com") {
		t.Error("should match *.google.com")
	}
	if m.match("google.com") {
		t.Error("should NOT match apex google.com")
	}
	if !m.match("github.com") {
		t.Error("should match github.com")
	}
	if m.match("www.github.com") {
		t.Error("should NOT match www.github.com")
	}
	if !m.match("www.youtube.com") {
		t.Error("should match *youtube.com")
	}
	if !m.match("youtube.com") {
		t.Error("should match *youtube.com (apex)")
	}
	if m.match("random.com") {
		t.Error("should NOT match random.com")
	}
}

func TestDomainMatcherWildcardAll(t *testing.T) {
	m := newDomainMatcher([]string{"*"})
	if !m.match("anything.com") {
		t.Error("* should match anything")
	}
	if !m.match("google.com") {
		t.Error("* should match google.com")
	}
}

func TestDomainMatcherNil(t *testing.T) {
	var m *domainMatcher
	if m.match("google.com") {
		t.Error("nil matcher should not match")
	}
	if m.count() != 0 {
		t.Error("nil matcher count should be 0")
	}
}

// --- SNI extraction -------------------------------------------------------

func TestExtractSNI(t *testing.T) {
	cases := []string{
		"www.example.com",
		"google.com",
		"mail.example.org",
		"a.b.c.d.example.com",
	}
	for _, sni := range cases {
		data := buildClientHello(sni)
		got := extractSNI(data)
		if got != sni {
			t.Errorf("extractSNI(buildClientHello(%q)) = %q, want %q", sni, got, sni)
		}
	}
}

func TestExtractSNINotTLS(t *testing.T) {
	// HTTP data — not a TLS record.
	data := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	if got := extractSNI(data); got != "" {
		t.Errorf("extractSNI on HTTP data = %q, want empty", got)
	}
}

func TestExtractSNIEmpty(t *testing.T) {
	if got := extractSNI(nil); got != "" {
		t.Errorf("extractSNI(nil) = %q, want empty", got)
	}
	if got := extractSNI([]byte{0x16}); got != "" {
		t.Errorf("extractSNI short = %q, want empty", got)
	}
}

// --- HTTP Host extraction -------------------------------------------------

func TestExtractHTTPHost(t *testing.T) {
	cases := []struct {
		data []byte
		want string
	}{
		{[]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"), "example.com"},
		{[]byte("GET / HTTP/1.1\r\nHost: example.com:8080\r\n\r\n"), "example.com"},
		{[]byte("POST /api HTTP/1.1\r\nhost: example.com\r\n\r\n"), "example.com"}, // lowercase
		{[]byte("CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"), "example.com"},
		{[]byte("GET / HTTP/1.1\r\n\r\n"), ""},            // no Host
		{[]byte("not http at all"), ""},                   // not HTTP
		{[]byte("Host: example.com\r\n"), "example.com"},  // Host first
	}
	for i, tc := range cases {
		got := extractHTTPHost(tc.data)
		if got != tc.want {
			t.Errorf("case %d: extractHTTPHost = %q, want %q", i, got, tc.want)
		}
	}
}

// --- extractDomain (combined) ---------------------------------------------

func TestExtractDomain(t *testing.T) {
	// TLS with SNI
	if got := extractDomain(buildClientHello("www.example.com")); got != "www.example.com" {
		t.Errorf("extractDomain TLS = %q", got)
	}
	// HTTP with Host
	httpData := []byte("GET / HTTP/1.1\r\nHost: example.org\r\n\r\n")
	if got := extractDomain(httpData); got != "example.org" {
		t.Errorf("extractDomain HTTP = %q", got)
	}
	// Neither
	if got := extractDomain([]byte("raw data")); got != "" {
		t.Errorf("extractDomain unknown = %q, want empty", got)
	}
}

// --- shouldProxy decision -------------------------------------------------

func TestShouldProxy(t *testing.T) {
	matcher := newDomainMatcher([]string{"*.google.com", "github.com"})

	// Split disabled → always proxy
	if !shouldProxy("random.com", false, matcher) {
		t.Error("split disabled should always proxy")
	}

	// Split enabled, domain in list → proxy
	if !shouldProxy("www.google.com", true, matcher) {
		t.Error("www.google.com should proxy (in list)")
	}
	if !shouldProxy("github.com", true, matcher) {
		t.Error("github.com should proxy (in list)")
	}

	// Split enabled, domain not in list → direct
	if shouldProxy("random.com", true, matcher) {
		t.Error("random.com should go direct (not in list)")
	}
	if shouldProxy("google.com", true, matcher) {
		t.Error("google.com apex should go direct (*.google.com doesn't match apex)")
	}
}

// --- parseDomainList ------------------------------------------------------

func TestParseDomainList(t *testing.T) {
	input := "  *.google.com  \n\n# comment\ngithub.com\n\n  \n*youtube.com\n"
	got := parseDomainList(input)
	want := []string{"*.google.com", "github.com", "*youtube.com"}
	if len(got) != len(want) {
		t.Fatalf("parseDomainList got %d items, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("parseDomainList[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestParseDomainListEmpty(t *testing.T) {
	got := parseDomainList("")
	if len(got) != 0 {
		t.Errorf("parseDomainList(\"\") = %v, want empty", got)
	}
	got = parseDomainList("# only comments\n\n")
	if len(got) != 0 {
		t.Errorf("parseDomainList comments only = %v, want empty", got)
	}
}
