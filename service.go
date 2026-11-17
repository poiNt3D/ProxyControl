package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ServiceDef describes one proxy service and its on-disk layout.
// The directory structure matches the bash version exactly so that
// existing systemd units and config files keep working unchanged.
type ServiceDef struct {
	Key         string // "wireproxy" or "byedpi"
	Label       string // human-readable
	UnitName    string // systemd user unit, e.g. "wireproxy.service"
	ConfigDir   string // ~/.config/proxycontrol/<key>/
	LogFile     string // …/<key>/<key>.log (if the unit appends to a file)
	ActiveConf  string // …/<key>/wireproxy.conf or byedpi.txt
	ConfExt     string // "conf" or "txt" — for listing profile files
	PortFile    string // byedpi only: …/byedpi/.port
	Template    string // wireproxy only: …/wireproxy/wireproxy.template
	StateFile   string // …/<key>/.active_profile
	ExcludeName string // profile name to exclude from the list (active config)
	HasProfiles bool   // whether this service supports config profile selection
	cfg         *Config
}

// NewService builds a ServiceDef for the given key, using cfg for the port.
func NewService(key string, cfg *Config) *ServiceDef {
	base := configDir()
	switch key {
	case "wireproxy":
		return &ServiceDef{
			Key:         "wireproxy",
			Label:       "WireProxy",
			UnitName:    "wireproxy.service",
			ConfigDir:   filepath.Join(base, "wireproxy"),
			LogFile:     filepath.Join(base, "wireproxy", "wireproxy.log"),
			ActiveConf:  filepath.Join(base, "wireproxy", "wireproxy.conf"),
			ConfExt:     "conf",
			Template:    filepath.Join(base, "wireproxy", "wireproxy.template"),
			StateFile:   filepath.Join(base, "wireproxy", ".active_profile"),
			ExcludeName: "wireproxy", // wireproxy.conf is the active config, not a profile
			HasProfiles: true,
			cfg:         cfg,
		}
	case "byedpi":
		return &ServiceDef{
			Key:         "byedpi",
			Label:       "ByeDPI",
			UnitName:    "byedpi.service",
			ConfigDir:   filepath.Join(base, "byedpi"),
			LogFile:     filepath.Join(base, "byedpi", "byedpi.log"),
			ActiveConf:  filepath.Join(base, "byedpi", "byedpi.txt"),
			ConfExt:     "txt",
			PortFile:    filepath.Join(base, "byedpi", ".port"),
			StateFile:   filepath.Join(base, "byedpi", ".active_profile"),
			ExcludeName: "", // all *.txt files are profiles; .active_profile is separate
			HasProfiles: true,
			cfg:         cfg,
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

// --- profile / config selection -------------------------------------------

// Profiles returns the sorted list of available config profile names.
// A profile is any file *.<ConfExt> in the service directory, minus the
// extension.  The active config name (ExcludeName) is filtered out so
// that e.g. wireproxy.conf does not appear as a selectable profile.
func (s *ServiceDef) Profiles() []string {
	entries, err := os.ReadDir(s.ConfigDir)
	if err != nil {
		return nil
	}
	ext := "." + s.ConfExt
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ext) {
			continue
		}
		profileName := strings.TrimSuffix(name, ext)
		if profileName == s.ExcludeName {
			continue
		}
		names = append(names, profileName)
	}
	sort.Strings(names)
	return names
}

// ActiveProfileName returns the name of the currently selected profile,
// or "" if no profile has been chosen yet.
//
// For wireproxy the .active_profile file stores the profile name directly.
// For byedpi it stores the profile *content* (command-line args), so we
// match it against each profile file to recover the name.
func (s *ServiceDef) ActiveProfileName() string {
	data, err := os.ReadFile(s.StateFile)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	if s.Key == "wireproxy" {
		return content
	}

	// byedpi: match content against profile files
	for _, profile := range s.Profiles() {
		pdata, err := os.ReadFile(filepath.Join(s.ConfigDir, profile+"."+s.ConfExt))
		if err == nil && strings.TrimSpace(string(pdata)) == content {
			return profile
		}
	}
	return ""
}

// ApplyProfile copies the named profile into the active config and restarts
// the service if it is currently running.  Mirrors the bash WP_apply_config
// and BD_apply_config functions.
func (s *ServiceDef) ApplyProfile(name string) error {
	profilePath := filepath.Join(s.ConfigDir, name+"."+s.ConfExt)
	profileData, err := os.ReadFile(profilePath)
	if err != nil {
		return err
	}

	_ = os.MkdirAll(s.ConfigDir, 0o755)

	switch s.Key {
	case "wireproxy":
		// 1. Profile content → active config (overwrite).
		if err := os.WriteFile(s.ActiveConf, profileData, 0o644); err != nil {
			return err
		}
		// 2. Append newline + template (BindAddress block).
		if tplData, err := os.ReadFile(s.Template); err == nil && len(tplData) > 0 {
			f, err := os.OpenFile(s.ActiveConf, os.O_APPEND|os.O_WRONLY, 0o644)
			if err == nil {
				f.Write([]byte("\n"))
				f.Write(tplData)
				f.Close()
			}
		}
		// 3. Save the profile name.
		_ = os.WriteFile(s.StateFile, []byte(name), 0o644)

	case "byedpi":
		// Profile content → .active_profile (consumed by the systemd unit).
		if err := os.WriteFile(s.StateFile, profileData, 0o644); err != nil {
			return err
		}
	}

	if s.IsActive() {
		return s.Restart()
	}
	return nil
}

// HasActiveConfig reports whether a profile has been selected and applied.
// Used by the Start button to warn the user if they need to pick a config
// first (same check as action_start_restart in the bash version).
func (s *ServiceDef) HasActiveConfig() bool {
	switch s.Key {
	case "wireproxy":
		info, err := os.Stat(s.ActiveConf)
		return err == nil && !info.IsDir() && info.Size() > 0
	case "byedpi":
		data, err := os.ReadFile(s.StateFile)
		return err == nil && len(strings.TrimSpace(string(data))) > 0
	}
	return true
}
