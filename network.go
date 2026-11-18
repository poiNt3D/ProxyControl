package main

import (
	"os/exec"
	"strconv"
	"strings"
)

// CheckNetwork tests whether the proxy on the given port can reach the
// internet.  proxyType is "socks5" (WireProxy, ByeDPI) or "http"
// (NoDPI).  It runs curl through the proxy with a short timeout — the
// same approach the bash version used.
//
// Returns a human-readable status string (in Russian, matching the
// original UI language).
func CheckNetwork(port int, proxyType string) string {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	var args []string
	if proxyType == "http" {
		args = []string{"--proxy", "http://" + addr}
	} else {
		args = []string{"--socks5-hostname", addr}
	}
	args = append(args,
		"-sI", // headers only, silent
		"--connect-timeout", "5",
		"--max-time", "8",
		"https://www.google.com",
	)
	cmd := exec.Command("curl", args...)
	label := strings.ToUpper(proxyType) // "SOCKS5" or "HTTP"
	if err := cmd.Run(); err != nil {
		return T("net.no_conn", label, port)
	}
	return T("net.connected", label, port)
}
