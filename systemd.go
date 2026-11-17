package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// systemdUserDir returns ~/.config/systemd/user — the directory where
// per-user unit files live and that systemctl --user reads from.
func systemdUserDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "systemd", "user")
}

// EnsureSystemdUnits checks whether the wireproxy, byedpi and resume
// monitor user units exist on disk.  Any that are missing are written
// from the templates below, systemctl --user daemon-reload is run so
// systemd picks them up, and the resume monitor is enabled and started
// so it watches for wake-from-sleep immediately.
//
// Existing unit files are left untouched — the user may have customized
// them.  Safe to call on every launch: every step is idempotent.
func EnsureSystemdUnits() {
	dir := systemdUserDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	// The resume helper script lives next to the configs so it is easy
	// to inspect or edit.  It is invoked by proxycontrol-resume.service.
	resumeScriptPath := filepath.Join(configDir(), resumeScriptName)

	created := false
	created = writeIfMissing(filepath.Join(dir, "wireproxy.service"), wireproxyUnit) || created
	created = writeIfMissing(filepath.Join(dir, "byedpi.service"), byedpiUnit) || created
	created = writeIfMissing(resumeScriptPath, resumeMonitorScript) || created
	created = writeIfMissing(filepath.Join(dir, resumeUnitName), resumeMonitorUnit) || created

	if created {
		// Tell systemd to re-read the unit files we just dropped in.
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}

	// The resume monitor is infrastructure, not a user-facing proxy —
	// make sure it is enabled and running on every launch.  enable and
	// start are both idempotent.  If dbus-monitor is absent the unit's
	// ConditionPathExists skips the start cleanly (no failure loop).
	_ = exec.Command("systemctl", "--user", "enable", resumeUnitName).Run()
	_ = exec.Command("systemctl", "--user", "start", resumeUnitName).Run()
}

// writeIfMissing writes content to path iff no file exists there yet.
// Returns true when the file was created.  Existing files are preserved
// so user customisations are never clobbered.
func writeIfMissing(path, content string) bool {
	if _, err := os.Stat(path); err == nil {
		return false
	}
	return os.WriteFile(path, []byte(content), 0o644) == nil
}

const (
	resumeScriptName = "proxycontrol-resume.sh"
	resumeUnitName   = "proxycontrol-resume.service"
)

// wireproxyUnit is the systemd user unit for the WireProxy SOCKS5 tunnel.
// The config and log paths match the GUI's layout
// (~/.config/proxycontrol/wireproxy/).  ExecStartPre truncates the log on
// each (re)start, StandardOutput/Error write to the same log file the GUI
// reads, and Restart=on-failure + RestartSec=5s recover from crashes.
const wireproxyUnit = `[Unit]
Description=WireProxy SOCKS5 tunnel (ProxyControl)
After=network.target

[Service]
Type=simple
ExecStartPre=-/bin/sh -c '> %h/.config/proxycontrol/wireproxy/wireproxy.log'
ExecStart=/usr/local/bin/wireproxy -c %h/.config/proxycontrol/wireproxy/wireproxy.conf
Restart=on-failure
RestartSec=5s
StandardOutput=file:%h/.config/proxycontrol/wireproxy/wireproxy.log
StandardError=file:%h/.config/proxycontrol/wireproxy/wireproxy.log

[Install]
WantedBy=default.target
`

// byedpiUnit is the systemd user unit for the ByeDPI SOCKS5 proxy.
// The port is read from .port (written by the Settings dialog); the
// profile args are read from .active_profile (written when the user
// picks a profile).  Both substitutions degrade gracefully when the
// files are absent, so the service starts even before a profile is
// chosen.
const byedpiUnit = `[Unit]
Description=ByeDPI SOCKS5 proxy (ProxyControl)
After=network.target

[Service]
Type=simple
ExecStartPre=-/bin/sh -c '> %h/.config/proxycontrol/byedpi/byedpi.log'
ExecStart=/bin/bash -c '/usr/local/bin/byedpi -i 127.0.0.1 -p $(cat %h/.config/proxycontrol/byedpi/.port 2>/dev/null || echo 8882) $(cat %h/.config/proxycontrol/byedpi/.active_profile 2>/dev/null)'
Restart=on-failure
RestartSec=5s
StandardOutput=file:%h/.config/proxycontrol/byedpi/byedpi.log
StandardError=file:%h/.config/proxycontrol/byedpi/byedpi.log

[Install]
WantedBy=default.target
`

// resumeMonitorScript is the shell helper run by proxycontrol-resume.service.
// It subscribes to the login1 PrepareForSleep D-Bus signal and restarts
// whichever proxy services were running before suspend, so WireProxy and
// ByeDPI recover automatically when the computer wakes up.
//
// dbus-monitor's default output is multi-line: the signal header line
// (containing member=PrepareForSleep) is followed by a line carrying the
// boolean argument — false means "resuming".  We arm a one-shot flag on
// the header line, then act on the boolean line.
//
// Only services that are still active get restarted, so a service the
// user had deliberately stopped stays stopped.
const resumeMonitorScript = `#!/bin/sh
# Restart ProxyControl proxy services when the system resumes from
# suspend.  Listens for the login1 PrepareForSleep D-Bus signal; the
# argument is "false" when the system is waking up.

dbus-monitor --system \
  "type='signal',interface='org.freedesktop.login1.Manager',member='PrepareForSleep'" 2>/dev/null |
while IFS= read -r line; do
	case "$line" in
		*member=PrepareForSleep*) waiting=true ;;
	esac
	if [ "$waiting" = true ]; then
		case "$line" in
			*boolean*false*)
				for svc in wireproxy.service byedpi.service; do
					if systemctl --user is-active --quiet "$svc" 2>/dev/null; then
						systemctl --user restart "$svc" 2>/dev/null
					fi
				done
				waiting=false
				;;
			*boolean*true*)
				waiting=false
				;;
		esac
	fi
done
`

// resumeMonitorUnit is the systemd user unit that runs the resume script.
// ConditionPathExists skips the start cleanly on systems without
// dbus-monitor (the monitor simply will not run, no failure loop).
// Restart=always keeps the monitor alive if dbus-monitor dies.
const resumeMonitorUnit = `[Unit]
Description=Restart ProxyControl proxies after suspend/resume
After=default.target
ConditionPathExists=/usr/bin/dbus-monitor

[Service]
Type=simple
ExecStart=/bin/sh %h/.config/proxycontrol/proxycontrol-resume.sh
Restart=always
RestartSec=10s

[Install]
WantedBy=default.target
`
