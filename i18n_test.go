package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectSystemLanguage verifies the LANG-based language detection.
func TestDetectSystemLanguage(t *testing.T) {
	cases := []struct {
		envs map[string]string
		want string
	}{
		{map[string]string{"LC_ALL": "ru_RU.UTF-8"}, "ru"},
		{map[string]string{"LC_ALL": "en_US.UTF-8"}, "en"},
		{map[string]string{"LANG": "de_DE.UTF-8"}, "de"},
		{map[string]string{"LANG": "C"}, "en"},
		{map[string]string{"LANG": "POSIX"}, "en"},
		{map[string]string{"LANG": ""}, "en"},
		{map[string]string{"LC_ALL": "fr_FR.UTF-8", "LANG": "ru_RU.UTF-8"}, "fr"},
	}
	saved := map[string]string{}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		saved[key] = os.Getenv(key)
		os.Unsetenv(key)
	}
	defer func() {
		for k, v := range saved {
			os.Setenv(k, v)
		}
	}()

	for i, tc := range cases {
		for k, v := range tc.envs {
			if v != "" {
				os.Setenv(k, v)
			} else {
				os.Unsetenv(k)
			}
		}
		got := detectSystemLanguage()
		if got != tc.want {
			t.Errorf("case %d: envs=%v want %s, got %s", i, tc.envs, tc.want, got)
		}
		os.Unsetenv("LC_ALL")
		os.Unsetenv("LC_MESSAGES")
		os.Unsetenv("LANG")
	}
}

// TestLoadLangStringsMerge verifies that on-disk overrides are merged
// on top of the built-in table (partial overrides work).
func TestLoadLangStringsMerge(t *testing.T) {
	dir := t.TempDir()
	override := `{"_name":"Custom English","btn.start":"▶ GO"}`
	if err := os.WriteFile(filepath.Join(dir, "en.json"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadLangStrings(dir, "en")
	if s["btn.start"] != "▶ GO" {
		t.Errorf("override not applied: btn.start = %q", s["btn.start"])
	}
	if s["btn.stop"] != enStrings["btn.stop"] {
		t.Errorf("built-in fallback failed: btn.stop = %q, want %q", s["btn.stop"], enStrings["btn.stop"])
	}
	if s["_name"] != "Custom English" {
		t.Errorf("_name override failed: %q", s["_name"])
	}
}

// TestListAvailableLangs verifies built-in + user-added files appear.
func TestListAvailableLangs(t *testing.T) {
	dir := t.TempDir()
	writeDefaultLangFile(dir, "en", enStrings)
	writeDefaultLangFile(dir, "ru", ruStrings)
	de := `{"_name":"Deutsch","btn.start":"▶ Starten"}`
	os.WriteFile(filepath.Join(dir, "de.json"), []byte(de), 0o644)

	langs := ListAvailableLangs(dir)
	if len(langs) != 3 {
		t.Fatalf("expected 3 languages, got %d: %v", len(langs), langs)
	}
	codes := []string{langs[0].Code, langs[1].Code, langs[2].Code}
	want := []string{"de", "en", "ru"}
	for i, c := range want {
		if codes[i] != c {
			t.Errorf("langs[%d].Code = %q, want %q (full: %v)", i, codes[i], c, codes)
		}
	}
	for _, l := range langs {
		switch l.Code {
		case "de":
			if l.Name != "Deutsch" {
				t.Errorf("de name = %q, want Deutsch", l.Name)
			}
		case "en":
			if l.Name != "English" {
				t.Errorf("en name = %q, want English", l.Name)
			}
		case "ru":
			if l.Name != "Русский" {
				t.Errorf("ru name = %q, want Русский", l.Name)
			}
		}
	}
}

// TestTFallback verifies T() falls back to English then to the key.
func TestTFallback(t *testing.T) {
	saved := currentStrings
	defer func() { currentStrings = saved }()

	currentStrings = loadLangStrings("", "ru")
	if got := T("btn.start"); got != "▶ Старт" {
		t.Errorf("T(btn.start) in ru = %q, want ▶ Старт", got)
	}
	currentStrings = loadLangStrings("", "en")
	if got := T("btn.start"); got != "▶ Start" {
		t.Errorf("T(btn.start) in en = %q, want ▶ Start", got)
	}
	if got := T("nonexistent.key"); got != "nonexistent.key" {
		t.Errorf("T(unknown) = %q, want the key", got)
	}
	got := T("status.svc_stopped", 8881)
	if got != "⏸ Service stopped (port :8881)" {
		t.Errorf("T(status.svc_stopped, 8881) = %q", got)
	}
}
