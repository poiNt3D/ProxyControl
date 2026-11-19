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
	defaultNDPort = 8881
	// Default binary locations — used when the config does not yet
	// record a path.  If the file is not actually present there the tab
	// shows the install buttons instead of the normal controls.
	defaultWPPath = "/usr/local/bin/wireproxy"
	defaultBDPath = "/usr/local/bin/byedpi"
	defaultNDPath = "/usr/local/bin/nodpi"
	// Default upstream repos.  WireProxy defaults to the AWG fork
	// (artem-russkikh/wireproxy-awg) which adds AmneziaWG support;
	// ByeDPI defaults to the official repo; NoDPI defaults to the
	// official GVCoder09/NoDPI repo.
	defaultWPRepo = "artem-russkikh/wireproxy-awg"
	defaultBDRepo = "hufrea/byedpi"
	defaultNDRepo = "GVCoder09/NoDPI"
)

// Config holds the persistent application settings.
// Stored as JSON at ~/.config/proxycontrol/proxycontrol.conf.
type Config struct {
	WPPort       int    `json:"wp_port"`
	BDPort       int    `json:"bd_port"`
	NDPort       int    `json:"nd_port"`
	WPPath       string `json:"wp_path"`       // wireproxy binary path
	BDPath       string `json:"bd_path"`       // byedpi binary path
	NDPath       string `json:"nd_path"`       // nodpi binary path
	WPRepo       string `json:"wp_repo"`       // wireproxy GitHub fork slug
	BDRepo       string `json:"bd_repo"`       // byedpi GitHub fork slug
	NDRepo       string `json:"nd_repo"`       // nodpi GitHub fork slug
	CheckUpdates bool     `json:"check_updates"` // check for binary updates on startup
	Language     string   `json:"language"`      // UI language code ("en", "ru", …)
	SplitEnabled bool `json:"split_enabled"` // combined-mode split tunneling
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
func combinedBackendPath() string { return filepath.Join(configDir(), ".combined_backend") }
func splitDomainsPath() string    { return filepath.Join(configDir(), ".split_domains") }

// LoadConfig reads the JSON config file.  If the file does not exist or is
// invalid JSON, it falls back to the old shell-sourceable format
// (WP_PORT=… / BD_PORT=…) used by the bash version, so upgrading preserves
// the user's previously chosen ports.
func LoadConfig() *Config {
	cfg := &Config{WPPort: defaultWPPort, BDPort: defaultBDPort, NDPort: defaultNDPort}

	dir := configDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "wireproxy"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "byedpi"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "nodpi"), 0o755)

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
	if cfg.NDPort < 1 || cfg.NDPort > 65535 {
		cfg.NDPort = defaultNDPort
	}
	if cfg.WPPath == "" {
		cfg.WPPath = defaultWPPath
	}
	if cfg.BDPath == "" {
		cfg.BDPath = defaultBDPath
	}
	if cfg.NDPath == "" {
		cfg.NDPath = defaultNDPath
	}
	if cfg.WPRepo == "" {
		cfg.WPRepo = defaultWPRepo
	}
	if cfg.BDRepo == "" {
		cfg.BDRepo = defaultBDRepo
	}
	if cfg.NDRepo == "" {
		cfg.NDRepo = defaultNDRepo
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

// LoadLastTab returns the last-selected tab.  "combined" is the first
// tab (the Общий combined-mode tab); the three service tabs follow.
func LoadLastTab() string {
	data, err := os.ReadFile(lastTabPath())
	if err == nil {
		tab := strings.TrimSpace(string(data))
		switch tab {
		case "combined", "wireproxy", "byedpi", "nodpi":
			return tab
		}
	}
	return "combined"
}

// SaveLastTab persists the active tab so it is restored on next launch.
func SaveLastTab(tab string) {
	_ = os.WriteFile(lastTabPath(), []byte(tab), 0o644)
}

// LoadCombinedBackend returns the persisted combined-mode backend key
// ("off", "wireproxy", "byedpi", or "nodpi"), or "" if unset.
func LoadCombinedBackend() string {
	data, err := os.ReadFile(combinedBackendPath())
	if err != nil {
		return ""
	}
	key := strings.TrimSpace(string(data))
	switch key {
	case "off", "wireproxy", "byedpi", "nodpi":
		return key
	}
	return ""
}

// SaveCombinedBackend persists the combined-mode backend selection.
func SaveCombinedBackend(key string) {
	_ = os.WriteFile(combinedBackendPath(), []byte(key), 0o644)
}

// LoadSplitDomains reads the split-tunnel domain pattern list from
// ~/.config/proxycontrol/.split_domains (one pattern per line; lines
// starting with # are comments).  Returns nil if the file does not
// exist.
func LoadSplitDomains() []string {
	data, err := os.ReadFile(splitDomainsPath())
	if err != nil {
		return nil
	}
	return parseDomainList(string(data))
}

// SaveSplitDomains writes the split-tunnel domain pattern list to
// ~/.config/proxycontrol/.split_domains (one pattern per line).
func SaveSplitDomains(domains []string) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, d := range domains {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	return os.WriteFile(splitDomainsPath(), []byte(b.String()), 0o644)
}

// PortForService returns the configured port for the given service key.
func (cfg *Config) PortForService(key string) int {
	switch key {
	case "byedpi":
		return cfg.BDPort
	case "nodpi":
		return cfg.NDPort
	}
	return cfg.WPPort
}

// SetPortForService updates the port for the given service key.
func (cfg *Config) SetPortForService(key string, port int) {
	switch key {
	case "byedpi":
		cfg.BDPort = port
	case "nodpi":
		cfg.NDPort = port
	default:
		cfg.WPPort = port
	}
}

// PathForService returns the configured binary path for the given
// service key.  Never empty — validate() fills in the default.
func (cfg *Config) PathForService(key string) string {
	switch key {
	case "byedpi":
		return cfg.BDPath
	case "nodpi":
		return cfg.NDPath
	}
	return cfg.WPPath
}

// SetPathForService updates the binary path for the given service key.
func (cfg *Config) SetPathForService(key, path string) {
	switch key {
	case "byedpi":
		cfg.BDPath = path
	case "nodpi":
		cfg.NDPath = path
	default:
		cfg.WPPath = path
	}
}

// RepoForService returns the configured GitHub fork slug ("owner/repo")
// for the given service key.  Never empty — validate() fills in the
// default.
func (cfg *Config) RepoForService(key string) string {
	switch key {
	case "byedpi":
		return cfg.BDRepo
	case "nodpi":
		return cfg.NDRepo
	}
	return cfg.WPRepo
}

// SetRepoForService updates the GitHub fork slug for the given service.
func (cfg *Config) SetRepoForService(key, slug string) {
	switch key {
	case "byedpi":
		cfg.BDRepo = slug
	case "nodpi":
		cfg.NDRepo = slug
	default:
		cfg.WPRepo = slug
	}
}

// String returns a human-readable summary (for debugging).
func (cfg *Config) String() string {
	return fmt.Sprintf("WP:%d BD:%d ND:%d lang=%s", cfg.WPPort, cfg.BDPort, cfg.NDPort, cfg.Language)
}
