package main

import (
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// combinedTab is the first tab (Общий).  It presents a radio group that
// selects which backend proxy the combined SOCKS5 listener on :8880
// chains to, plus a split-tunneling control: when enabled, only domains
// in the user's pattern list are forwarded through the proxy; all other
// traffic gets a direct connection.
type combinedTab struct {
	cm       *CombinedMode
	services map[string]*ServiceDef
	win      fyne.Window
	cfg      *Config

	radio      *widget.RadioGroup
	status     *widget.Label
	splitCheck *widget.Check
	splitLabel *widget.Label
	content    fyne.CanvasObject

	suppress bool // true during programmatic SetSelected
}

// newCombinedTab builds the Combined tab.  The persisted backend is
// restored visually but not applied here; call Restore after the window
// is shown so any start-up error dialog is visible.  Split-tunnel
// settings are applied to the CombinedMode immediately.
func newCombinedTab(cm *CombinedMode, services map[string]*ServiceDef, win fyne.Window, cfg *Config) *combinedTab {
	t := &combinedTab{cm: cm, services: services, win: win, cfg: cfg}

	title := widget.NewLabel(T("combined.title"))
	title.TextStyle = fyne.TextStyle{Bold: true}

	t.status = widget.NewLabel(T("combined.status_off"))
	t.status.TextStyle = fyne.TextStyle{Italic: true}

	t.radio = widget.NewRadioGroup(
		[]string{T("combined.off"), "WireProxy", "ByeDPI", "NoDPI"},
		func(sel string) {
			if t.suppress {
				return
			}
			t.applySelection(sel)
		},
	)
	t.radio.Horizontal = false

	// Split-tunneling controls.  Create splitLabel first so the check
	// callback (fired by SetChecked below) can safely call
	// refreshSplitStatus.
	t.splitLabel = widget.NewLabel("")
	t.splitLabel.TextStyle = fyne.TextStyle{Italic: true}

	t.splitCheck = widget.NewCheck(T("split.enable"), func(on bool) {
		if t.suppress {
			return
		}
		t.cfg.SplitEnabled = on
		t.applySplit()
		_ = t.cfg.Save()
		t.refreshSplitStatus()
	})

	splitBtn := widget.NewButton(T("split.domains_btn"), func() {
		t.showSplitDomainsDialog()
	})

	splitRow := container.NewHBox(t.splitCheck, splitBtn)

	// Apply persisted split settings to the CombinedMode now so they are
	// active before the listener starts.
	t.applySplit()

	// Set the checkbox without triggering the callback (suppress is
	// still true from the radio init below, but set it here too for
	// clarity).
	t.suppress = true
	t.splitCheck.SetChecked(cfg.SplitEnabled)
	t.suppress = false
	t.refreshSplitStatus()

	// Show the persisted selection in the radio without triggering apply.
	saved := LoadCombinedBackend()
	t.suppress = true
	t.radio.SetSelected(labelForKey(saved))
	t.suppress = false

	t.content = container.NewVBox(title, t.status, t.radio, splitRow, t.splitLabel)
	return t
}

// Content returns the root canvas object for this tab.
func (t *combinedTab) Content() fyne.CanvasObject { return t.content }

// Restore applies the persisted backend selection.  Call after the
// window is shown so any error dialog is visible.  Safe to call from a
// goroutine.
func (t *combinedTab) Restore(key string) {
	if key == "" || key == "off" {
		t.refreshStatus()
		return
	}
	if err := t.cm.SetBackend(t.services, key); err != nil {
		dialog.ShowError(err, t.win)
		t.suppress = true
		t.radio.SetSelected(T("combined.off"))
		t.suppress = false
	}
	t.refreshStatus()
}

// applySelection is the radio OnChanged handler: maps the label to a
// service key, applies it, persists the choice, and refreshes status.
// On error the radio is reverted to "Выкл".
func (t *combinedTab) applySelection(sel string) {
	key := keyForLabel(sel)
	if err := t.cm.SetBackend(t.services, key); err != nil {
		dialog.ShowError(err, t.win)
		t.suppress = true
		t.radio.SetSelected(T("combined.off"))
		t.suppress = false
		SaveCombinedBackend("off")
		t.refreshStatus()
		return
	}
	SaveCombinedBackend(key)
	t.refreshStatus()
}

// applySplit pushes the current split-tunnel settings to the
// CombinedMode.  Called on startup and whenever the user toggles the
// checkbox or edits the domain list.
func (t *combinedTab) applySplit() {
	t.cm.SetSplit(t.cfg.SplitEnabled, t.cfg.SplitDomains)
}

// refreshStatus updates the one-line status label from the current
// combined-mode state and backend service activity.
func (t *combinedTab) refreshStatus() {
	backend := t.cm.Backend()
	if backend == "off" || backend == "" {
		t.status.SetText(T("combined.status_off"))
		return
	}
	svc := t.services[backend]
	if svc == nil {
		t.status.SetText(T("combined.status_off_short"))
		return
	}
	if svc.IsActive() {
		t.status.SetText(T("combined.status_active", combinedPort, svc.Label, svc.Port()))
	} else {
		t.status.SetText(T("combined.status_stopped", svc.Label))
	}
}

// refreshSplitStatus updates the split-tunnel status label.
func (t *combinedTab) refreshSplitStatus() {
	if !t.cfg.SplitEnabled {
		t.splitLabel.SetText("")
		return
	}
	n := len(t.cfg.SplitDomains)
	t.splitLabel.SetText(T("split.status_on", n))
}

// showSplitDomainsDialog opens a modal with a multi-line text entry for
// editing the domain pattern list.  Each line is one pattern; lines
// starting with # are ignored.  On Apply the list is parsed, saved to
// config, and applied to the CombinedMode.
func (t *combinedTab) showSplitDomainsDialog() {
	entry := widget.NewMultiLineEntry()
	entry.SetMinRowsVisible(12)
	entry.Wrapping = fyne.TextWrapOff
	entry.SetText(strings.Join(t.cfg.SplitDomains, "\n"))

	hint := widget.NewLabel(T("split.hint"))
	hint.TextStyle = fyne.TextStyle{Italic: true}

	content := container.NewVBox(hint, entry)

	var dlg *dialog.CustomDialog
	applyBtn := widget.NewButton(T("btn.apply"), func() {
		domains := parseDomainList(entry.Text)
		t.cfg.SplitDomains = domains
		t.applySplit()
		_ = t.cfg.Save()
		t.refreshSplitStatus()
		dlg.Hide()
	})
	cancelBtn := widget.NewButton(T("btn.cancel"), func() {
		dlg.Hide()
	})

	bottom := container.NewHBox(cancelBtn, applyBtn)
	dialogContent := container.NewBorder(nil, bottom, nil, nil, content)

	dlg = dialog.NewCustomWithoutButtons(T("split.domains_title"), dialogContent, t.win)
	dlg.Resize(fyne.NewSize(480, 400))
	dlg.Show()
}

// parseDomainList splits the text area content into clean domain
// patterns: one per line, trimmed, blank lines and comments (#) removed.
func parseDomainList(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// statusLoop periodically refreshes the status label so it reflects
// backend stops/starts made from the service's own tab.
func (t *combinedTab) statusLoop() {
	tk := time.NewTicker(2 * time.Second)
	defer tk.Stop()
	for range tk.C {
		t.refreshStatus()
	}
}

// --- label ↔ key mapping --------------------------------------------------

func keyForLabel(label string) string {
	switch label {
	case "WireProxy":
		return "wireproxy"
	case "ByeDPI":
		return "byedpi"
	case "NoDPI":
		return "nodpi"
	}
	return "off"
}

func labelForKey(key string) string {
	switch key {
	case "wireproxy":
		return "WireProxy"
	case "byedpi":
		return "ByeDPI"
	case "nodpi":
		return "NoDPI"
	}
	return T("combined.off")
}
