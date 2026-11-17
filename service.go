package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ServiceDef describes one proxy service and its on-disk layout.
// The directory structure matches the bash version exactly so that
// existing systemd units and config files keep working unchanged.
type ServiceDef struct {
	Key        string // "wireproxy" or "byedpi"
	Label      string // human-readable
	UnitName   string // systemd user unit, e.g. "wireproxy.service"
	ConfigDir  string // ~/.config/proxycontrol/<key>/
	LogFile    string // …/<key>/<key>.log (if the unit appends to a file)
	ActiveConf string // …/<key>/wireproxy.conf or byedpi.txt
	ConfExt    string // "conf" or "txt" — for listing profile files
	PortFile   string // byedpi only: …/byedpi/.port
	Template   string // wireproxy only: …/wireproxy/wireproxy.template
	cfg        *Config
}

// NewService builds a ServiceDef for the given key, using cfg for the port.
func NewService(key string, cfg *Config) *ServiceDef {
	base := configDir()
	switch key {
	case "wireproxy":
		return &ServiceDef{
			Key:        "wireproxy",
			Label:      "WireProxy",
			UnitName:   "wireproxy.service",
			ConfigDir:  filepath.Join(base, "wireproxy"),
			LogFile:    filepath.Join(base, "wireproxy", "wireproxy.log"),
			ActiveConf: filepath.Join(base, "wireproxy", "wireproxy.conf"),
			ConfExt:    "conf",
			Template:   filepath.Join(base, "wireproxy", "wireproxy.template"),
			cfg:        cfg,
		}
	case "byedpi":
		return &ServiceDef{
			Key:        "byedpi",
			Label:      "ByeDPI",
			UnitName:   "byedpi.service",
			ConfigDir:  filepath.Join(base, "byedpi"),
			LogFile:    filepath.Join(base, "byedpi", "byedpi.log"),
			ActiveConf: filepath.Join(base, "byedpi", "byedpi.txt"),
			ConfExt:    "txt",
			PortFile:   filepath.Join(base, "byedpi", ".port"),
			cfg:        cfg,
		}
	}
	return nil
}

// Port returns the currently configured port for this service.
func (s *ServiceDef) Port() int { return s.cfg.PortForService(s.Key) }

// --- systemd (user) wrappers ----------------------------------------------

func (s *ServiceDef) runSystemctl(action string) error {
	return exec.Command("systemctl", "--user", action, s.UnitName).Run()
}

// IsActive reports whether the service is currently running.
func (s *ServiceDef) IsActive() bool {
	out, err := exec.Command("systemctl", "--user", "is-active", s.UnitName).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

// IsEnabled reports whether the service is enabled for autostart.
func (s *ServiceDef) IsEnabled() bool {
	out, err := exec.Command("systemctl", "--user", "is-enabled", s.UnitName).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "enabled"
}

func (s *ServiceDef) Start() error   { return s.runSystemctl("start") }
func (s *ServiceDef) Stop() error    { return s.runSystemctl("stop") }
func (s *ServiceDef) Restart() error { return s.runSystemctl("restart") }
func (s *ServiceDef) Enable() error  { return s.runSystemctl("enable") }
func (s *ServiceDef) Disable() error { return s.runSystemctl("disable") }

// --- log reading -----------------------------------------------------------

// RecentLog returns up to n recent log lines.  It tries the log file first
// (used when the systemd unit has StandardOutput=append:), then falls back
// to journalctl.
func (s *ServiceDef) RecentLog(n int) string {
	if data, err := os.ReadFile(s.LogFile); err == nil && len(data) > 0 {
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		return strings.Join(lines, "\n")
	}
	out, err := exec.Command("journalctl", "--user", "-u", s.UnitName,
		"--no-pager", "-n", strconv.Itoa(n), "-o", "cat").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// --- port / config management ---------------------------------------------

// ApplyPort persists a new port to the service's config files and restarts
// the unit if it is currently active.  This mirrors the bash version's
// apply_settings behaviour.
func (s *ServiceDef) ApplyPort(port int) error {
	s.cfg.SetPortForService(s.Key, port)

	switch s.Key {
	case "wireproxy":
		// Update BindAddress in both template and active config.
		s.updateWPBindAddress(port)
	case "byedpi":
		// Write the .port file consumed by the systemd unit.
		_ = os.MkdirAll(s.ConfigDir, 0o755)
		_ = os.WriteFile(s.PortFile, []byte(strconv.Itoa(port)), 0o644)
	}

	if s.IsActive() {
		return s.Restart()
	}
	return nil
}

// updateWPBindAddress rewrites the BindAddress line in wireproxy.template
// and wireproxy.conf (if it contains a [Socks5] section).
func (s *ServiceDef) updateWPBindAddress(port int) {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for _, path := range []string{s.Template, s.ActiveConf} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		hasSocks5 := false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[Socks5]") {
				hasSocks5 = true
			}
			if strings.HasPrefix(trimmed, "BindAddress") {
				if s.isActiveConf(path) && !hasSocks5 {
					continue // only update .conf if it has [Socks5]
				}
				lines[i] = "BindAddress = " + addr
			}
		}
		_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	}
}

func (s *ServiceDef) isActiveConf(path string) bool {
	return path == s.ActiveConf
}

// OpenConfigDir opens the service's config directory in the default
// file manager (xdg-open).
func (s *ServiceDef) OpenConfigDir() {
	_ = exec.Command("xdg-open", s.ConfigDir).Start()
}
