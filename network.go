package main

import (
	"fmt"
	"os/exec"
	"strconv"
)

// CheckNetwork tests whether the SOCKS5 proxy on the given port can
// reach the internet.  It runs curl through the proxy with a short
// timeout — the same approach the bash version used.
//
// Returns a human-readable status string (in Russian, matching the
// original UI language).
func CheckNetwork(port int) string {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command("curl",
		"--socks5-hostname", addr,
		"-sI",               // headers only, silent
		"--connect-timeout", "5",
		"--max-time", "8",
		"https://www.google.com",
	)
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("❌ НЕТ СОЕДИНЕНИЯ (SOCKS5 :%d не отвечает)", port)
	}
	return fmt.Sprintf("⚡ ПОДКЛЮЧЕНО (SOCKS5 :%d работает)", port)
}
