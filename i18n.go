package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// i18n.go — internationalisation for ProxyControl.
//
// Two languages are built in (English and Russian); their string tables
// are embedded in the binary and ALSO written to
// ~/.config/proxycontrol/lang/{en,ru}.json on first launch so users can
// override individual strings.  Users may drop additional <code>.json
// files into that directory to add new languages; they appear in the
// settings drop-down automatically.
//
// On first launch the system language is detected (from LC_ALL /
// LC_MESSAGES / LANG) and written to the config.  If detection fails or
// the detected language is not built in, English is used.  Changing the
// language in settings takes effect on next launch.

// LangDir returns the directory for language override files.
func LangDir() string { return filepath.Join(configDir(), "lang") }

// LangInfo is one entry in the language drop-down.
type LangInfo struct {
	Code string // "en", "ru", "de", …
	Name string // "English", "Русский", …
}

// builtinLangNames is the display name for each built-in language.
// User-added languages read their "_name" field from the JSON file.
var builtinLangNames = map[string]string{
	"en": "English",
	"ru": "Русский",
}

// builtInLangs is the ordered list of built-in language codes.
var builtInLangs = []string{"en", "ru"}

// currentStrings holds the active language's string table, loaded once
// at startup by InitLang.
var currentStrings map[string]string

// --- built-in string tables ----------------------------------------------

// Each table is a map of dotted key → translated string.  The special
// key "_name" holds the display name shown in the drop-down.

var enStrings = map[string]string{
	"_name": "English",

	// app / window
	"app.title": "ProxyControl",

	// tab labels
	"tab.combined": "Combined",
	"tab.wireproxy": "WireProxy",
	"tab.byedpi":    "ByeDPI",
	"tab.nodpi":     "NoDPI",

	// combined mode
	"combined.title":             "Combined mode",
	"combined.off":               "Off",
	"combined.status_off":        "⏸ Off (port :8880 free)",
	"combined.status_off_short":  "⏸ Off",
	"combined.status_active":     "⚡ 127.0.0.1:%d → %s :%d",
	"combined.status_stopped":    "⚠ %s stopped — connections via :8880 will fail",

	// service status panel
	"status.stopped":        "Stopped",
	"status.running":        "Running",
	"status.port":           "Port",
	"status.autostart":      "Autostart",
	"status.autostart_on":   "On",
	"status.autostart_off":  "Off",
	"status.svc_stopped":    "⏸ Service stopped (port :%d)",

	// network check
	"net.no_conn":   "❌ NO CONNECTION (%s :%d not responding)",
	"net.connected": "⚡ CONNECTED (%s :%d working)",

	// log panel
	"log.collapsed": "Log ▸",
	"log.expanded":  "Log ▾",
	"log.empty":     "(log empty — service is running but produces no output)",

	// config dropdown
	"config.label":           "Config",
	"config.placeholder":     "(none)",
	"config.no_config_title": "%s: no config selected",
	"config.no_config_body":  "No active config found.\n\nPlease select a profile from the dropdown on the right first.",

	// control buttons
	"btn.start":          "▶ Start",
	"btn.restart":        "♻ Restart",
	"btn.stop":           "⏹ Stop",
	"btn.settings":       "⚙ Settings",
	"btn.open":           "Open",
	"btn.apply":          "Apply",
	"btn.cancel":         "Cancel",
	"btn.download":       "Download",
	"btn.close":          "Close",
	"btn.select_file":    "📁 Select file",
	"btn.download_github": "⬇ Download from GitHub",

	// install mode
	"install.binary_not_found":  "⚠ %s: binary not found",
	"install.fetching_release":  "Fetching release info…",
	"install.downloading":       "Downloading %s",
	"install.downloading_tag":   "Downloading %s…",
	"install.where_to_install":  "Where to install the binary?",
	"install.user_dir":          "📂 %s (no sudo)",
	"install.system_dir":        "🔐 %s (sudo)",
	"install.installing":        "Installing…",
	"install.copying":           "Copying file…",
	"install.not_a_file":        "selected path is not a file",

	// settings dialog
	"settings.title":             "ProxyControl Settings",
	"settings.language":          "Language",
	"settings.autostart_check":   "enable autostart",
	"settings.check_updates":     "check for binary updates on startup",
	"settings.autostart_wp":      "WireProxy autostart",
	"settings.port_wp":           "WireProxy port (SOCKS5)",
	"settings.fork_wp":           "WireProxy fork (GitHub)",
	"settings.folder_wp":         "WireProxy folder",
	"settings.autostart_bd":      "ByeDPI autostart",
	"settings.port_bd":           "ByeDPI port (SOCKS5)",
	"settings.fork_bd":           "ByeDPI fork (GitHub)",
	"settings.folder_bd":         "ByeDPI folder",
	"settings.autostart_nd":      "NoDPI autostart",
	"settings.port_nd":           "NoDPI port (HTTP)",
	"settings.fork_nd":           "NoDPI fork (GitHub)",
	"settings.folder_nd":         "NoDPI folder",
	"settings.updates":           "Updates",
	"settings.port_not_number":   "must be a number",
	"settings.port_range":        "port must be 1–65535",
	"settings.lang_changed":      "Language changed. Restart the application for it to take effect.",
	"settings.restart":           "Restart",

	// update dialog
	"update.available": "Binary updates available:",
	"update.title":     "Updates",
	"update.info":      "%s:  %s → %s",

	// error messages (user-visible)
	"err.unknown_service":  "unknown service: %s",
	"err.cannot_start":     "failed to start %s: %v",
	"err.cannot_listen":    "failed to listen on port :%d: %v",
	"err.http_proxy_reject": "HTTP proxy %s rejected CONNECT: %s",
	"err.fetch_release":    "failed to fetch release info for %s",
	"err.arch_not_found":   "no archive for architecture %s found (available: %s)",
	"err.binary_not_in_arch": "binary %q not found in archive",
	"err.no_privilege":     "neither pkexec nor sudo available — cannot install to /usr/local/bin",
}

var ruStrings = map[string]string{
	"_name": "Русский",

	// app / window
	"app.title": "ProxyControl",

	// tab labels
	"tab.combined": "Общий",
	"tab.wireproxy": "WireProxy",
	"tab.byedpi":    "ByeDPI",
	"tab.nodpi":     "NoDPI",

	// combined mode
	"combined.title":             "Общий режим",
	"combined.off":               "Выкл",
	"combined.status_off":        "⏸ Выкл (порт :8880 свободен)",
	"combined.status_off_short":  "⏸ Выкл",
	"combined.status_active":     "⚡ 127.0.0.1:%d → %s :%d",
	"combined.status_stopped":    "⚠ %s остановлен — соединения через :8880 не пройдут",

	// service status panel
	"status.stopped":        "Остановлен",
	"status.running":        "Запущен",
	"status.port":           "Порт",
	"status.autostart":      "Автозапуск",
	"status.autostart_on":   "Вкл",
	"status.autostart_off":  "Выкл",
	"status.svc_stopped":    "⏸ Сервис остановлен (порт :%d)",

	// network check
	"net.no_conn":   "❌ НЕТ СОЕДИНЕНИЯ (%s :%d не отвечает)",
	"net.connected": "⚡ ПОДКЛЮЧЕНО (%s :%d работает)",

	// log panel
	"log.collapsed": "Лог ▸",
	"log.expanded":  "Лог ▾",
	"log.empty":     "(лог пуст — сервис работает, но не выводит сообщения)",

	// config dropdown
	"config.label":           "Конфиг",
	"config.placeholder":     "(не выбран)",
	"config.no_config_title": "%s: не выбран конфиг",
	"config.no_config_body":  "Активный конфиг не найден.\n\nСначала выберите профиль в выпадающем списке справа.",

	// control buttons
	"btn.start":          "▶ Старт",
	"btn.restart":        "♻ Рестарт",
	"btn.stop":           "⏹ Стоп",
	"btn.settings":       "⚙ Настройки",
	"btn.open":           "Открыть",
	"btn.apply":          "Применить",
	"btn.cancel":         "Отмена",
	"btn.download":       "Скачать",
	"btn.close":          "Закрыть",
	"btn.select_file":    "📁 Выбрать файл",
	"btn.download_github": "⬇ Скачать с GitHub",

	// install mode
	"install.binary_not_found":  "⚠ %s: бинарный файл не найден",
	"install.fetching_release":  "Получение информации о релизе…",
	"install.downloading":       "Скачивание %s",
	"install.downloading_tag":   "Скачивание %s…",
	"install.where_to_install":  "Куда установить бинарный файл?",
	"install.user_dir":          "📂 %s (без sudo)",
	"install.system_dir":        "🔐 %s (sudo)",
	"install.installing":        "Установка…",
	"install.copying":           "Копирование файла…",
	"install.not_a_file":        "выбранный путь не является файлом",

	// settings dialog
	"settings.title":             "Настройки ProxyControl",
	"settings.language":          "Язык",
	"settings.autostart_check":   "включить автозапуск",
	"settings.check_updates":     "проверять обновления бинарных файлов при запуске",
	"settings.autostart_wp":      "Автозапуск WireProxy",
	"settings.port_wp":           "Порт WireProxy (SOCKS5)",
	"settings.fork_wp":           "Форк WireProxy (GitHub)",
	"settings.folder_wp":         "Папка WireProxy",
	"settings.autostart_bd":      "Автозапуск ByeDPI",
	"settings.port_bd":           "Порт ByeDPI (SOCKS5)",
	"settings.fork_bd":           "Форк ByeDPI (GitHub)",
	"settings.folder_bd":         "Папка ByeDPI",
	"settings.autostart_nd":      "Автозапуск NoDPI",
	"settings.port_nd":           "Порт NoDPI (HTTP)",
	"settings.fork_nd":           "Форк NoDPI (GitHub)",
	"settings.folder_nd":         "Папка NoDPI",
	"settings.updates":           "Обновления",
	"settings.port_not_number":   "должно быть число",
	"settings.port_range":        "порт должен быть 1–65535",
	"settings.lang_changed":      "Язык изменён. Перезапустите приложение, чтобы изменения вступили в силу.",
	"settings.restart":           "Перезапуск",

	// update dialog
	"update.available": "Доступны обновления бинарных файлов:",
	"update.title":     "Обновления",
	"update.info":      "%s:  %s → %s",

	// error messages (user-visible)
	"err.unknown_service":  "неизвестный сервис: %s",
	"err.cannot_start":     "не удалось запустить %s: %v",
	"err.cannot_listen":    "не удалось слушать порт :%d: %v",
	"err.http_proxy_reject": "HTTP-прокси %s отклонил CONNECT: %s",
	"err.fetch_release":    "не удалось получить информацию о релизе %s",
	"err.arch_not_found":   "архив для архитектуры %s не найден (доступно: %s)",
	"err.binary_not_in_arch": "бинарный файл %q не найден в архиве",
	"err.no_privilege":     "ни pkexec, ни sudo не доступны — нельзя установить в /usr/local/bin",
}

// builtinStrings returns the built-in string table for code, or nil.
func builtinStrings(code string) map[string]string {
	switch code {
	case "en":
		return enStrings
	case "ru":
		return ruStrings
	}
	return nil
}

// --- detection ------------------------------------------------------------

// detectSystemLanguage tries to determine the user's language from the
// LC_ALL / LC_MESSAGES / LANG environment variables.  Returns the
// 2-letter language code (e.g. "ru"), or "en" if detection fails.
func detectSystemLanguage() string {
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		val := os.Getenv(env)
		if val == "" || val == "C" || val == "POSIX" {
			continue
		}
		// "ru_RU.UTF-8" → "ru",  "en_US.UTF-8" → "en"
		base := strings.SplitN(val, ".", 2)[0]
		base = strings.SplitN(base, "_", 2)[0]
		base = strings.ToLower(base)
		if base != "" && base != "c" {
			return base
		}
	}
	return "en"
}

// --- initialisation -------------------------------------------------------

// InitLang prepares the language directory, writes the built-in
// language files to disk (without overwriting existing ones), detects
// and persists the system language on first launch, and loads the
// active string table.
func InitLang(cfg *Config) {
	langDir := LangDir()
	_ = os.MkdirAll(langDir, 0o755)

	// Write built-in language files to disk so users can see and
	// override them.  Never overwrite an existing file.
	writeDefaultLangFile(langDir, "en", enStrings)
	writeDefaultLangFile(langDir, "ru", ruStrings)

	// Detect + persist on first launch (Language is "" until set).
	if cfg.Language == "" {
		detected := detectSystemLanguage()
		// If the detected language is not built in and no user file
		// exists for it, fall back to English.
		if builtinStrings(detected) == nil && !langFileExists(langDir, detected) {
			detected = "en"
		}
		cfg.Language = detected
		_ = cfg.Save()
	}

	currentStrings = loadLangStrings(langDir, cfg.Language)
}

// writeDefaultLangFile writes strings as <langDir>/<code>.json if it
// does not already exist.
func writeDefaultLangFile(langDir, code string, strings map[string]string) {
	path := filepath.Join(langDir, code+".json")
	if _, err := os.Stat(path); err == nil {
		return // already exists — don't clobber user customisations
	}
	data, err := json.MarshalIndent(strings, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// langFileExists reports whether <langDir>/<code>.json exists.
func langFileExists(langDir, code string) bool {
	_, err := os.Stat(filepath.Join(langDir, code+".json"))
	return err == nil
}

// loadLangStrings loads the string table for code.  The built-in table
// (if any) is the base; the on-disk file is overlaid on top so users
// can override individual keys without rewriting the whole file.
func loadLangStrings(langDir, code string) map[string]string {
	base := builtinStrings(code)
	if base == nil {
		base = map[string]string{}
	} else {
		// Copy so we don't mutate the package-level map.
		out := make(map[string]string, len(base))
		for k, v := range base {
			out[k] = v
		}
		base = out
	}
	path := filepath.Join(langDir, code+".json")
	if data, err := os.ReadFile(path); err == nil {
		var disk map[string]string
		if json.Unmarshal(data, &disk) == nil {
			for k, v := range disk {
				base[k] = v
			}
		}
	}
	return base
}

// --- translate ------------------------------------------------------------

// T translates a key to the active language.  Unknown keys fall back
// to the built-in English table, then to the key itself.  Extra args
// are passed to fmt.Sprintf.
func T(key string, args ...interface{}) string {
	s, ok := currentStrings[key]
	if !ok {
		s, ok = enStrings[key]
		if !ok {
			return key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// --- listing --------------------------------------------------------------

// ListAvailableLangs returns the built-in languages plus any
// user-added <code>.json files in langDir, sorted by code.
func ListAvailableLangs(langDir string) []LangInfo {
	seen := map[string]bool{}
	var langs []LangInfo

	// Built-in languages first.
	for _, code := range builtInLangs {
		name := builtinLangNames[code]
		// Let an on-disk override change the display name.
		if n := readLangName(langDir, code); n != "" {
			name = n
		}
		langs = append(langs, LangInfo{Code: code, Name: name})
		seen[code] = true
	}

	// User-added languages.
	entries, err := os.ReadDir(langDir)
	if err != nil {
		sortLangs(langs)
		return langs
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(e.Name(), ".json")
		if seen[code] {
			continue
		}
		name := readLangName(langDir, code)
		if name == "" {
			name = code
		}
		langs = append(langs, LangInfo{Code: code, Name: name})
		seen[code] = true
	}

	sortLangs(langs)
	return langs
}

// readLangName reads the "_name" field from <langDir>/<code>.json.
func readLangName(langDir, code string) string {
	path := filepath.Join(langDir, code+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	return m["_name"]
}

func sortLangs(langs []LangInfo) {
	sort.Slice(langs, func(i, j int) bool {
		return langs[i].Code < langs[j].Code
	})
}

// LangNameToCode looks up the code for a display name in the given list.
func LangNameToCode(langs []LangInfo, name string) string {
	for _, l := range langs {
		if l.Name == name {
			return l.Code
		}
	}
	return ""
}

// LangCodeToName looks up the display name for a code.
func LangCodeToName(langs []LangInfo, code string) string {
	for _, l := range langs {
		if l.Code == code {
			return l.Name
		}
	}
	return code
}
