package main

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// combinedTab is the first tab (Общий).  It presents a radio group that
// selects which backend proxy the combined SOCKS5 listener on :8880
// chains to.  Selecting a backend starts that service (if needed) and
// (re)starts the listener; selecting "Выкл" stops it.
//
// The tab is intentionally minimal — just the radio control plus a
// one-line status showing where :8880 is currently forwarding.
type combinedTab struct {
	cm       *CombinedMode
	services map[string]*ServiceDef
	win      fyne.Window

	radio    *widget.RadioGroup
	status   *widget.Label
	content  fyne.CanvasObject

	suppress bool // true during programmatic SetSelected
}

// newCombinedTab builds the Combined tab.  The persisted backend is
// restored visually but not applied here; call Restore after the window
// is shown so any start-up error dialog is visible.
func newCombinedTab(cm *CombinedMode, services map[string]*ServiceDef, win fyne.Window) *combinedTab {
	t := &combinedTab{cm: cm, services: services, win: win}

	title := widget.NewLabel("Общий режим")
	title.TextStyle = fyne.TextStyle{Bold: true}

	t.status = widget.NewLabel("⏸ Выкл (порт :8880 свободен)")
	t.status.TextStyle = fyne.TextStyle{Italic: true}

	t.radio = widget.NewRadioGroup(
		[]string{"Выкл", "WireProxy", "ByeDPI", "NoDPI"},
		func(sel string) {
			if t.suppress {
				return
			}
			t.applySelection(sel)
		},
	)
	t.radio.Horizontal = false

	// Show the persisted selection in the radio without triggering apply.
	saved := LoadCombinedBackend()
	t.suppress = true
	t.radio.SetSelected(labelForKey(saved))
	t.suppress = false

	t.content = container.NewVBox(title, t.status, t.radio)
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
		t.radio.SetSelected("Выкл")
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
		t.radio.SetSelected("Выкл")
		t.suppress = false
		SaveCombinedBackend("off")
		t.refreshStatus()
		return
	}
	SaveCombinedBackend(key)
	t.refreshStatus()
}

// refreshStatus updates the one-line status label from the current
// combined-mode state and backend service activity.
func (t *combinedTab) refreshStatus() {
	backend := t.cm.Backend()
	if backend == "off" || backend == "" {
		t.status.SetText("⏸ Выкл (порт :8880 свободен)")
		return
	}
	svc := t.services[backend]
	if svc == nil {
		t.status.SetText("⏸ Выкл")
		return
	}
	if svc.IsActive() {
		t.status.SetText(fmt.Sprintf("⚡ 127.0.0.1:%d → %s :%d",
			combinedPort, svc.Label, svc.Port()))
	} else {
		t.status.SetText(fmt.Sprintf("⚠ %s остановлен — соединения через :8880 не пройдут", svc.Label))
	}
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
	return "Выкл"
}
