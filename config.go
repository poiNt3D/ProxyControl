package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultWPPort = 8883
	defaultBDPort = 8882
	// Default binary locations — used when the config does not yet
	// record a path.  If the file is not actually present there the tab
	// shows the install buttons instead of the normal controls.
	defaultWPPath = "/usr/local/bin/wireproxy"
	defaultBDPath = "/usr/local/bin/byedpi"
	// Default upstream repos.  WireProxy defaults to the AWG fork
	// (artem-russkikh/wireproxy-awg) which adds AmneziaWG support;
	// ByeDPI defaults to the official repo.
	defaultWPRepo = "artem-russkikh/wireproxy-awg"
	defaultBDRepo = "hufrea/byedpi"
)

// Config holds the persistent application settings.
// Stored as JSON at ~/.config/proxycontrol/proxycontrol.conf.
type Config struct {
	WPPort       int    `json:"wp_port"`
	BDPort       int    `json:"bd_port"`
	WPPath       string `json:"wp_path"`       // wireproxy binary path
	BDPath       string `json:"bd_path"`       // byedpi binary path
	WPRepo       string `json:"wp_repo"`       // wireproxy GitHub fork slug
	BDRepo       string `json:"bd_repo"`       // byedpi GitHub fork slug
	CheckUpdates bool   `json:"check_updates"` // check for binary updates on startup
}

// configDir returns ~/.config/proxycontrol (the same dir the bash version used).
func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".config", "proxycontrol")
}

func configPath() string    { return filepath.Join(configDir(), "proxycontrol.conf") }
func lastTabPath() string   { return filepath.Join(configDir(), ".last_tab") }

// LoadConfig reads the JSON config file.  If the file does not exist or is
// invalid JSON, it falls back to the old shell-sourceable format
// (WP_PORT=… / BD_PORT=…) used by the bash version, so upgrading preserves
// the user's previously chosen ports.
func LoadConfig() *Config {
	cfg := &Config{WPPort: defaultWPPort, BDPort: defaultBDPort}

	dir := configDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "wireproxy"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "byedpi"), 0o755)

	data, err := os.ReadFile(configPath())
	if err == nil {
		// Try JSON first.
		if json.Unmarshal(data, cfg) == nil {
			cfg.validate()
			return cfg
		}
		// Fall back: old shell format (key=value lines).
		parseShellConfig(string(data), cfg)
	}
	cfg.validate()
	return cfg
}

func parseShellConfig(text string, cfg *Config) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		port, err := strconv.Atoi(val)
		if err != nil {
			continue
		}
		switch key {
		case "WP_PORT":
			cfg.WPPort = port
		case "BD_PORT":
			cfg.BDPort = port
		}
	}
}

func (cfg *Config) validate() {
	if cfg.WPPort < 1 || cfg.WPPort > 65535 {
		cfg.WPPort = defaultWPPort
	}
	if cfg.BDPort < 1 || cfg.BDPort > 65535 {
		cfg.BDPort = defaultBDPort
	}
	if cfg.WPPath == "" {
		cfg.WPPath = defaultWPPath
	}
	if cfg.BDPath == "" {
		cfg.BDPath = defaultBDPath
	}
	if cfg.WPRepo == "" {
		cfg.WPRepo = defaultWPRepo
	}
	if cfg.BDRepo == "" {
		cfg.BDRepo = defaultBDRepo
	}
}

// Save writes the config as pretty-printed JSON.
func (cfg *Config) Save() error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0o644)
}

// LoadLastTab returns the last-selected tab ("wireproxy" or "byedpi").
func LoadLastTab() string {
	data, err := os.ReadFile(lastTabPath())
	if err == nil {
		tab := strings.TrimSpace(string(data))
		if tab == "wireproxy" || tab == "byedpi" {
			return tab
		}
	}
	return "wireproxy"
}

// SaveLastTab persists the active tab so it is restored on next launch.
func SaveLastTab(tab string) {
	_ = os.WriteFile(lastTabPath(), []byte(tab), 0o644)
}

// PortForService returns the configured port for the given service key.
func (cfg *Config) PortForService(key string) int {
	if key == "byedpi" {
		return cfg.BDPort
	}
	return cfg.WPPort
}

// SetPortForService updates the port for the given service key.
func (cfg *Config) SetPortForService(key string, port int) {
	if key == "byedpi" {
		cfg.BDPort = port
	} else {
		cfg.WPPort = port
	}
}

// PathForService returns the configured binary path for the given
// service key.  Never empty — validate() fills in the default.
func (cfg *Config) PathForService(key string) string {
	if key == "byedpi" {
		return cfg.BDPath
	}
	return cfg.WPPath
}

// SetPathForService updates the binary path for the given service key.
func (cfg *Config) SetPathForService(key, path string) {
	if key == "byedpi" {
		cfg.BDPath = path
	} else {
		cfg.WPPath = path
	}
}

// RepoForService returns the configured GitHub fork slug ("owner/repo")
// for the given service key.  Never empty — validate() fills in the
// default.
func (cfg *Config) RepoForService(key string) string {
	if key == "byedpi" {
		return cfg.BDRepo
	}
	return cfg.WPRepo
}

// SetRepoForService updates the GitHub fork slug for the given service.
func (cfg *Config) SetRepoForService(key, slug string) {
	if key == "byedpi" {
		cfg.BDRepo = slug
	} else {
		cfg.WPRepo = slug
	}
}

// String returns a human-readable summary (for debugging).
func (cfg *Config) String() string {
	return fmt.Sprintf("WP:%d BD:%d", cfg.WPPort, cfg.BDPort)
}
