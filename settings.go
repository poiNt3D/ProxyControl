package main

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
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
	ndSvc := NewService("nodpi", cfg)

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

	// --- NoDPI controls ---
	ndAutoCheck := widget.NewCheck("включить автозапуск", nil)
	ndAutoCheck.SetChecked(ndSvc.IsEnabled())

	ndPortEntry := widget.NewEntry()
	ndPortEntry.SetText(strconv.Itoa(cfg.NDPort))
	ndPortEntry.Validator = portValidator

	ndRepoEntry := widget.NewEntry()
	ndRepoEntry.SetText(cfg.NDRepo)
	ndRepoEntry.PlaceHolder = "owner/repo"

	ndFolderBtn := widget.NewButton("Открыть", func() {
		ndSvc.OpenConfigDir()
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
		{Text: "Автозапуск NoDPI", Widget: ndAutoCheck},
		{Text: "Порт NoDPI (HTTP)", Widget: ndPortEntry},
		{Text: "Форк NoDPI (GitHub)", Widget: ndRepoEntry},
		{Text: "Папка NoDPI", Widget: ndFolderBtn},
		{Text: "Обновления", Widget: checkUpdatesCheck},
	}

	// Build a scrollable list of labelled rows.  dialog.NewForm does not
	// scroll its content, so with 13 items the bottom rows are clipped
	// by the action buttons on smaller screens.
	var rows []fyne.CanvasObject
	for _, item := range items {
		label := widget.NewLabel(item.Text)
		rows = append(rows, container.NewBorder(nil, nil, label, nil, item.Widget))
	}
	scroll := container.NewVScroll(container.NewVBox(rows...))

	// Declare dlg up front so the button closures can hide it.
	var dlg *dialog.CustomDialog

	applyBtn := widget.NewButton("Применить", func() {
		dlg.Hide()
		applySettings(cfg, wpSvc, bdSvc, ndSvc,
			wpAutoCheck.Checked, wpPortEntry.Text, wpRepoEntry.Text,
			bdAutoCheck.Checked, bdPortEntry.Text, bdRepoEntry.Text,
			ndAutoCheck.Checked, ndPortEntry.Text, ndRepoEntry.Text,
			checkUpdatesCheck.Checked)
		onApplied()
	})
	cancelBtn := widget.NewButton("Отмена", func() {
		dlg.Hide()
	})

	bottomBar := container.NewHBox(cancelBtn, applyBtn)
	dialogContent := container.NewBorder(nil, bottomBar, nil, nil, scroll)

	dlg = dialog.NewCustomWithoutButtons("Настройки ProxyControl", dialogContent, win)
	dlg.Resize(fyne.NewSize(560, 520))
	dlg.Show()
}

// applySettings persists port, fork and update-check changes, applies
// port changes to the service config files (restarting if active), and
// enables/disables autostart.
func applySettings(cfg *Config, wpSvc, bdSvc, ndSvc *ServiceDef,
	wpAuto bool, wpPortStr, wpRepo string,
	bdAuto bool, bdPortStr, bdRepo string,
	ndAuto bool, ndPortStr, ndRepo string,
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

	// NoDPI port
	if ndPort, err := strconv.Atoi(ndPortStr); err == nil && ndPort >= 1 && ndPort <= 65535 {
		if ndPort != cfg.NDPort {
			_ = ndSvc.ApplyPort(ndPort)
		}
	}

	// Fork slugs (strip whitespace; empty falls back to default on next load)
	cfg.WPRepo = strings.TrimSpace(wpRepo)
	cfg.BDRepo = strings.TrimSpace(bdRepo)
	cfg.NDRepo = strings.TrimSpace(ndRepo)
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
	if ndAuto {
		_ = ndSvc.Enable()
	} else {
		_ = ndSvc.Disable()
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
