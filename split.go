package main

import (
	"bytes"
	"net"
	"regexp"
	"strings"
	"sync"
)

// split.go — split-tunnel domain matching for combined mode.
//
// When split tunneling is enabled, the combined SOCKS5 listener peeks at
// each connection's target domain (from the SOCKS5 CONNECT request, or
// from the TLS SNI / HTTP Host header when the client sends an IP) and
// forwards through the backend proxy only if the domain matches a
// pattern in the user's list.  Non-matching domains get a direct
// connection, bypassing the proxy entirely.
//
// Wildcard patterns: `*` matches any sequence of characters.
//   - google.com         exact match (apex only)
//   - *.google.com       any subdomain (www.google.com, mail.google.com, …)
//   - *google.com        anything ending in google.com (incl. the apex)
//   - *                  match everything

// --- domain matching ------------------------------------------------------

// matchDomain reports whether domain matches the glob pattern.  Both
// are lower-cased and trimmed.  `*` in the pattern matches any run of
// characters; a pattern without `*` requires an exact match.
func matchDomain(pattern, domain string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if pattern == "" || domain == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == domain
	}
	// Convert the glob to a regex: escape regex metacharacters, then
	// turn the escaped `\*` back into `.*`.
	regexStr := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), "\\*", ".*") + "$"
	matched, err := regexp.MatchString(regexStr, domain)
	if err != nil {
		return false
	}
	return matched
}

// domainInList reports whether domain matches any pattern in list.
func domainInList(domain string, list []string) bool {
	for _, p := range list {
		if matchDomain(p, domain) {
			return true
		}
	}
	return false
}

// --- compiled matcher (cached for performance) ----------------------------

// domainMatcher caches compiled regexes so repeated connections don't
// re-compile the same patterns.  The cache is keyed by the pattern
// string; it is rebuilt whenever the user edits the domain list.
type domainMatcher struct {
	mu       sync.RWMutex
	patterns []compiledPattern
}

type compiledPattern struct {
	raw   string
	exact string // non-empty when the pattern has no wildcard
	re    *regexp.Regexp
}

// newDomainMatcher compiles a list of glob patterns into a matcher.
// Patterns that contain no `*` are stored as plain strings for a fast
// map lookup; wildcard patterns are compiled to regexps.
func newDomainMatcher(patterns []string) *domainMatcher {
	m := &domainMatcher{}
	for _, raw := range patterns {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if !strings.Contains(raw, "*") {
			m.patterns = append(m.patterns, compiledPattern{raw: raw, exact: raw})
		} else if raw == "*" {
			m.patterns = append(m.patterns, compiledPattern{raw: raw, exact: "__all__"})
		} else {
			regexStr := "^" + strings.ReplaceAll(regexp.QuoteMeta(raw), "\\*", ".*") + "$"
			if re, err := regexp.Compile(regexStr); err == nil {
				m.patterns = append(m.patterns, compiledPattern{raw: raw, re: re})
			}
		}
	}
	return m
}

// match reports whether domain matches any compiled pattern.
func (m *domainMatcher) match(domain string) bool {
	if m == nil {
		return false
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.patterns {
		switch {
		case p.exact == "__all__":
			return true
		case p.exact != "":
			if p.exact == domain {
				return true
			}
		default:
			if p.re != nil && p.re.MatchString(domain) {
				return true
			}
		}
	}
	return false
}

// count returns the number of compiled patterns.
func (m *domainMatcher) count() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.patterns)
}

// --- SNI / Host extraction ------------------------------------------------

// extractDomain examines the first application-data packet and returns
// the domain it carries: the TLS SNI for HTTPS traffic, or the Host
// header for plaintext HTTP.  Returns "" if no domain can be extracted.
func extractDomain(data []byte) string {
	if d := extractSNI(data); d != "" {
		return d
	}
	return extractHTTPHost(data)
}

// extractSNI parses a TLS ClientHello record and returns the Server
// Name Indication (SNI) hostname.  Returns "" if data is not a valid
// ClientHello or contains no SNI extension.
//
// TLS record layout:
//
//	0x16 (handshake), version(2), length(2)
//	handshake: 0x01 (client_hello), length(3), version(2), random(32),
//	           session_id_len(1)+session_id, cipher_suites_len(2)+data,
//	           compression_len(1)+data, extensions_len(2)+extensions
//	extension: type(2), length(2), data
//	SNI (type 0x0000): list_len(2), [name_type(1), name_len(2), name]...
func extractSNI(data []byte) string {
	// TLS record header.
	if len(data) < 5 || data[0] != 0x16 {
		return ""
	}
	recLen := int(data[3])<<8 | int(data[4])
	if len(data) < 5+recLen {
		return ""
	}
	d := data[5 : 5+recLen]

	// Handshake header.
	if len(d) < 4 || d[0] != 0x01 {
		return ""
	}
	hsLen := int(d[1])<<16 | int(d[2])<<8 | int(d[3])
	if len(d) < 4+hsLen {
		return ""
	}
	d = d[4 : 4+hsLen]

	// Skip client version (2) + random (32).
	if len(d) < 34 {
		return ""
	}
	d = d[34:]

	// Session ID.
	if len(d) < 1 {
		return ""
	}
	sidLen := int(d[0])
	d = d[1:]
	if len(d) < sidLen {
		return ""
	}
	d = d[sidLen:]

	// Cipher suites.
	if len(d) < 2 {
		return ""
	}
	csLen := int(d[0])<<8 | int(d[1])
	d = d[2:]
	if len(d) < csLen {
		return ""
	}
	d = d[csLen:]

	// Compression methods.
	if len(d) < 1 {
		return ""
	}
	cmLen := int(d[0])
	d = d[1:]
	if len(d) < cmLen {
		return ""
	}
	d = d[cmLen:]

	// Extensions.
	if len(d) < 2 {
		return ""
	}
	extTotal := int(d[0])<<8 | int(d[1])
	d = d[2:]
	if len(d) < extTotal {
		return ""
	}
	d = d[:extTotal]

	// Walk extensions looking for SNI (type 0x0000).
	for len(d) >= 4 {
		extType := int(d[0])<<8 | int(d[1])
		extLen := int(d[2])<<8 | int(d[3])
		d = d[4:]
		if len(d) < extLen {
			return ""
		}
		if extType == 0x0000 {
			return parseSNIExtension(d[:extLen])
		}
		d = d[extLen:]
	}
	return ""
}

// parseSNIExtension parses the server_name extension data and returns
// the first host_name entry (type 0x00).
func parseSNIExtension(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	listLen := int(data[0])<<8 | int(data[1])
	d := data[2:]
	if len(d) < listLen {
		return ""
	}
	d = d[:listLen]
	for len(d) >= 3 {
		nameType := d[0]
		nameLen := int(d[1])<<8 | int(d[2])
		d = d[3:]
		if len(d) < nameLen {
			return ""
		}
		if nameType == 0x00 { // host_name
			return string(d[:nameLen])
		}
		d = d[nameLen:]
	}
	return ""
}

// extractHTTPHost parses a plaintext HTTP request and returns the
// Host header value (without port).  Returns "" if no Host header is
// found.
func extractHTTPHost(data []byte) string {
	lower := bytes.ToLower(data)
	idx := bytes.Index(lower, []byte("host:"))
	if idx < 0 {
		return ""
	}
	start := idx + 5 // skip "host:"
	for start < len(data) && (data[start] == ' ' || data[start] == '\t') {
		start++
	}
	end := start
	for end < len(data) && data[end] != '\r' && data[end] != '\n' {
		end++
	}
	host := strings.TrimSpace(string(data[start:end]))
	// Strip the port if present (Host: example.com:8080).
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

// --- split-tunnel decision ------------------------------------------------

// shouldProxy decides whether a connection to the given domain should
// go through the backend proxy (true) or connect directly (false).
// When split tunneling is disabled, always returns true.
func shouldProxy(domain string, splitEnabled bool, matcher *domainMatcher) bool {
	if !splitEnabled {
		return true
	}
	return matcher.match(domain)
}
