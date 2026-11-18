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

// showSettingsDialog opens a modal form with a language selector,
// autostart checkboxes, port entries, fork-slug fields, an update-check
// toggle, and "open folder" buttons for each service.  When the user
// clicks Apply the changes are persisted and applied to systemd; if the
// language changed, a restart notice is shown.
func showSettingsDialog(win fyne.Window, cfg *Config, onApplied func()) {
	wpSvc := NewService("wireproxy", cfg)
	bdSvc := NewService("byedpi", cfg)
	ndSvc := NewService("nodpi", cfg)

	// --- Language selector ---
	langs := ListAvailableLangs(LangDir())
	langNames := make([]string, len(langs))
	for i, l := range langs {
		langNames[i] = l.Name
	}
	langSelect := widget.NewSelect(langNames, nil)
	langSelect.SetSelected(LangCodeToName(langs, cfg.Language))
	if langSelect.Selected == "" && len(langNames) > 0 {
		langSelect.SetSelected(langNames[0])
	}

	// --- WireProxy controls ---
	wpAutoCheck := widget.NewCheck(T("settings.autostart_check"), nil)
	wpAutoCheck.SetChecked(wpSvc.IsEnabled())

	wpPortEntry := widget.NewEntry()
	wpPortEntry.SetText(strconv.Itoa(cfg.WPPort))
	wpPortEntry.Validator = portValidator

	wpRepoEntry := widget.NewEntry()
	wpRepoEntry.SetText(cfg.WPRepo)
	wpRepoEntry.PlaceHolder = "owner/repo"

	wpFolderBtn := widget.NewButton(T("btn.open"), func() {
		wpSvc.OpenConfigDir()
	})

	// --- ByeDPI controls ---
	bdAutoCheck := widget.NewCheck(T("settings.autostart_check"), nil)
	bdAutoCheck.SetChecked(bdSvc.IsEnabled())

	bdPortEntry := widget.NewEntry()
	bdPortEntry.SetText(strconv.Itoa(cfg.BDPort))
	bdPortEntry.Validator = portValidator

	bdRepoEntry := widget.NewEntry()
	bdRepoEntry.SetText(cfg.BDRepo)
	bdRepoEntry.PlaceHolder = "owner/repo"

	bdFolderBtn := widget.NewButton(T("btn.open"), func() {
		bdSvc.OpenConfigDir()
	})

	// --- NoDPI controls ---
	ndAutoCheck := widget.NewCheck(T("settings.autostart_check"), nil)
	ndAutoCheck.SetChecked(ndSvc.IsEnabled())

	ndPortEntry := widget.NewEntry()
	ndPortEntry.SetText(strconv.Itoa(cfg.NDPort))
	ndPortEntry.Validator = portValidator

	ndRepoEntry := widget.NewEntry()
	ndRepoEntry.SetText(cfg.NDRepo)
	ndRepoEntry.PlaceHolder = "owner/repo"

	ndFolderBtn := widget.NewButton(T("btn.open"), func() {
		ndSvc.OpenConfigDir()
	})

	// --- Update check ---
	checkUpdatesCheck := widget.NewCheck(T("settings.check_updates"), nil)
	checkUpdatesCheck.SetChecked(cfg.CheckUpdates)

	items := []*widget.FormItem{
		{Text: T("settings.language"), Widget: langSelect},
		{Text: T("settings.autostart_wp"), Widget: wpAutoCheck},
		{Text: T("settings.port_wp"), Widget: wpPortEntry},
		{Text: T("settings.fork_wp"), Widget: wpRepoEntry},
		{Text: T("settings.folder_wp"), Widget: wpFolderBtn},
		{Text: T("settings.autostart_bd"), Widget: bdAutoCheck},
		{Text: T("settings.port_bd"), Widget: bdPortEntry},
		{Text: T("settings.fork_bd"), Widget: bdRepoEntry},
		{Text: T("settings.folder_bd"), Widget: bdFolderBtn},
		{Text: T("settings.autostart_nd"), Widget: ndAutoCheck},
		{Text: T("settings.port_nd"), Widget: ndPortEntry},
		{Text: T("settings.fork_nd"), Widget: ndRepoEntry},
		{Text: T("settings.folder_nd"), Widget: ndFolderBtn},
		{Text: T("settings.updates"), Widget: checkUpdatesCheck},
	}

	// Build a scrollable list of labelled rows.  dialog.NewForm does not
	// scroll its content, so with 14 items the bottom rows are clipped
	// by the action buttons on smaller screens.
	var rows []fyne.CanvasObject
	for _, item := range items {
		label := widget.NewLabel(item.Text)
		rows = append(rows, container.NewBorder(nil, nil, label, nil, item.Widget))
	}
	scroll := container.NewVScroll(container.NewVBox(rows...))

	// Declare dlg up front so the button closures can hide it.
	var dlg *dialog.CustomDialog

	applyBtn := widget.NewButton(T("btn.apply"), func() {
		dlg.Hide()
		newLang := LangNameToCode(langs, langSelect.Selected)
		langChanged := newLang != "" && newLang != cfg.Language
		applySettings(cfg, wpSvc, bdSvc, ndSvc,
			wpAutoCheck.Checked, wpPortEntry.Text, wpRepoEntry.Text,
			bdAutoCheck.Checked, bdPortEntry.Text, bdRepoEntry.Text,
			ndAutoCheck.Checked, ndPortEntry.Text, ndRepoEntry.Text,
			checkUpdatesCheck.Checked, newLang)
		onApplied()
		if langChanged {
			dialog.ShowInformation(T("settings.restart"), T("settings.lang_changed"), win)
		}
	})
	cancelBtn := widget.NewButton(T("btn.cancel"), func() {
		dlg.Hide()
	})

	bottomBar := container.NewHBox(cancelBtn, applyBtn)
	dialogContent := container.NewBorder(nil, bottomBar, nil, nil, scroll)

	dlg = dialog.NewCustomWithoutButtons(T("settings.title"), dialogContent, win)
	dlg.Resize(fyne.NewSize(560, 520))
	dlg.Show()
}

// applySettings persists language, port, fork and update-check changes,
// applies port changes to the service config files (restarting if
// active), and enables/disables autostart.
func applySettings(cfg *Config, wpSvc, bdSvc, ndSvc *ServiceDef,
	wpAuto bool, wpPortStr, wpRepo string,
	bdAuto bool, bdPortStr, bdRepo string,
	ndAuto bool, ndPortStr, ndRepo string,
	checkUpdates bool, language string) {

	// Language
	if language != "" {
		cfg.Language = language
	}

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
		return fmt.Errorf(T("settings.port_not_number"))
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf(T("settings.port_range"))
	}
	return nil
}
