package main

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// showSettingsDialog opens a modal form with autostart checkboxes, port
// entries, fork-slug fields, an update-check toggle, and "open folder"
// buttons for each service.  When the user clicks "Применить" the changes
// are persisted and applied to systemd.
func showSettingsDialog(win fyne.Window, cfg *Config, onApplied func()) {
	wpSvc := NewService("wireproxy", cfg)
	bdSvc := NewService("byedpi", cfg)

	// --- WireProxy controls ---
	wpAutoCheck := widget.NewCheck("включить автозапуск", nil)
	wpAutoCheck.SetChecked(wpSvc.IsEnabled())

	wpPortEntry := widget.NewEntry()
	wpPortEntry.SetText(strconv.Itoa(cfg.WPPort))
	wpPortEntry.Validator = portValidator

	wpRepoEntry := widget.NewEntry()
	wpRepoEntry.SetText(cfg.WPRepo)
	wpRepoEntry.PlaceHolder = "owner/repo"

	wpFolderBtn := widget.NewButton("Открыть", func() {
		wpSvc.OpenConfigDir()
	})

	// --- ByeDPI controls ---
	bdAutoCheck := widget.NewCheck("включить автозапуск", nil)
	bdAutoCheck.SetChecked(bdSvc.IsEnabled())

	bdPortEntry := widget.NewEntry()
	bdPortEntry.SetText(strconv.Itoa(cfg.BDPort))
	bdPortEntry.Validator = portValidator

	bdRepoEntry := widget.NewEntry()
	bdRepoEntry.SetText(cfg.BDRepo)
	bdRepoEntry.PlaceHolder = "owner/repo"

	bdFolderBtn := widget.NewButton("Открыть", func() {
		bdSvc.OpenConfigDir()
	})

	// --- Update check ---
	checkUpdatesCheck := widget.NewCheck("проверять обновления бинарных файлов при запуске", nil)
	checkUpdatesCheck.SetChecked(cfg.CheckUpdates)

	items := []*widget.FormItem{
		{Text: "Автозапуск WireProxy", Widget: wpAutoCheck},
		{Text: "Порт WireProxy (SOCKS5)", Widget: wpPortEntry},
		{Text: "Форк WireProxy (GitHub)", Widget: wpRepoEntry},
		{Text: "Папка WireProxy", Widget: wpFolderBtn},
		{Text: "Автозапуск ByeDPI", Widget: bdAutoCheck},
		{Text: "Порт ByeDPI (SOCKS5)", Widget: bdPortEntry},
		{Text: "Форк ByeDPI (GitHub)", Widget: bdRepoEntry},
		{Text: "Папка ByeDPI", Widget: bdFolderBtn},
		{Text: "Обновления", Widget: checkUpdatesCheck},
	}

	d := dialog.NewForm(
		"Настройки ProxyControl",
		"Применить", "Отмена",
		items,
		func(confirmed bool) {
			if !confirmed {
				return
			}
			applySettings(cfg, wpSvc, bdSvc,
				wpAutoCheck.Checked, wpPortEntry.Text, wpRepoEntry.Text,
				bdAutoCheck.Checked, bdPortEntry.Text, bdRepoEntry.Text,
				checkUpdatesCheck.Checked)
			onApplied()
		},
		win,
	)
	d.Resize(fyne.NewSize(520, 460))
	d.Show()
}

// applySettings persists port, fork and update-check changes, applies
// port changes to the service config files (restarting if active), and
// enables/disables autostart.
func applySettings(cfg *Config, wpSvc, bdSvc *ServiceDef,
	wpAuto bool, wpPortStr, wpRepo string,
	bdAuto bool, bdPortStr, bdRepo string,
	checkUpdates bool) {

	// WireProxy port
	if wpPort, err := strconv.Atoi(wpPortStr); err == nil && wpPort >= 1 && wpPort <= 65535 {
		if wpPort != cfg.WPPort {
			_ = wpSvc.ApplyPort(wpPort)
		}
	}

	// ByeDPI port
	if bdPort, err := strconv.Atoi(bdPortStr); err == nil && bdPort >= 1 && bdPort <= 65535 {
		if bdPort != cfg.BDPort {
			_ = bdSvc.ApplyPort(bdPort)
		}
	}

	// Fork slugs (strip whitespace; empty falls back to default on next load)
	cfg.WPRepo = strings.TrimSpace(wpRepo)
	cfg.BDRepo = strings.TrimSpace(bdRepo)
	cfg.CheckUpdates = checkUpdates

	// Save config (JSON)
	_ = cfg.Save()

	// Autostart (enable/disable systemd user units)
	if wpAuto {
		_ = wpSvc.Enable()
	} else {
		_ = wpSvc.Disable()
	}
	if bdAuto {
		_ = bdSvc.Enable()
	} else {
		_ = bdSvc.Disable()
	}
}

// portValidator validates that the entry text is a port number 1–65535.
func portValidator(s string) error {
	port, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("должно быть число")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("порт должен быть 1–65535")
	}
	return nil
}
