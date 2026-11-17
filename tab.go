package main

import (
	"fmt"
	"image/color"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// Window heights for expanded / collapsed log modes.  The collapsible
// log header toggles between these so the window physically shrinks when
// the log is hidden.  The collapsed height fits status + header + buttons.
const (
	logExpandedHeight  = 480
	logCollapsedHeight = 150
)

// serviceTab holds the UI widgets for one service tab and runs the
// background refresh goroutines that keep them up to date.
type serviceTab struct {
	svc        *ServiceDef
	cfg        *Config
	win        fyne.Window
	onSettings func()
	onToggleLog func()

	// Status panel: separate field widgets instead of one pipe-joined string.
	stateDot  *canvas.Circle // colored circle: green=running, red=stopped
	stateText *widget.Label
	portVal   *widget.Label
	autoVal   *widget.Label

	network     *widget.Label
	topPanel    fyne.CanvasObject // status row + network label (reused in buildContent)
	buttonsRow  fyne.CanvasObject // control buttons + config dropdown (reused)
	logLabel    *widget.Label
	logScroll   *container.Scroll
	logHeader   *widget.Button // collapsible header bar (placeholder when collapsed)

	// Inline config dropdown (right-aligned on the button row).
	configSelect   *widget.Select
	suppressConfig bool // true during programmatic SetSelected

	startBtn *widget.Button

	content fyne.CanvasObject

	// networkMu prevents overlapping network checks.
	netMu   sync.Mutex
	netBusy bool
}

// newStatusField builds a labelled key+value pair: a dim key label and a
// bold value label side by side.  Returns the value label (so it can be
// updated at runtime) and the container holding both.
func newStatusField(key string) (*widget.Label, fyne.CanvasObject) {
	k := widget.NewLabel(key)
	v := widget.NewLabel("")
	v.TextStyle = fyne.TextStyle{Bold: true}
	return v, container.NewHBox(k, v)
}

// newServiceTab creates the tab UI and starts its refresh goroutines.
func newServiceTab(svc *ServiceDef, cfg *Config, win fyne.Window,
	onSettings func(), onToggleLog func()) *serviceTab {

	t := &serviceTab{
		svc:         svc,
		cfg:         cfg,
		win:         win,
		onSettings:  onSettings,
		onToggleLog: onToggleLog,
	}

	// --- Status panel (structured fields, no pipe separators) ---
	titleLabel := widget.NewLabel(svc.Label)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Colored status indicator: a canvas.Circle in a fixed-size cell so
	// the HBox reserves space for it (canvas primitives have zero MinSize).
	// The HBox top-aligns children, so the 14px circle would sit at the
	// top of the ~22px text row. A transparent 6px spacer above it (plus
	// VBox padding ≈10px total) shifts it down to align with the text.
	t.stateDot = canvas.NewCircle(color.NRGBA{R: 200, G: 40, B: 40, A: 255}) // red initially
	dotInner := container.NewGridWrap(fyne.NewSize(14, 14), t.stateDot)
	dotSpacer := container.NewGridWrap(fyne.NewSize(14, 6),
		canvas.NewRectangle(color.Transparent))
	dotCell := container.NewVBox(dotSpacer, dotInner)

	t.stateText = widget.NewLabel("Остановлен")
	t.stateText.TextStyle = fyne.TextStyle{Bold: true}

	portVal, portField := newStatusField("Порт")
	t.portVal = portVal
	autoVal, autoField := newStatusField("Автозапуск")
	t.autoVal = autoVal

	statusRow := container.NewHBox(
		titleLabel,
		dotCell,
		t.stateText,
		layout.NewSpacer(),
		portField,
		autoField,
	)

	t.network = widget.NewLabel("")
	t.topPanel = container.NewVBox(statusRow, t.network)

	// --- Log panel (collapsible) ---
	// The header bar is always visible — it acts as the placeholder when
	// collapsed and as the title bar when expanded.  Clicking it toggles
	// the log scroll visibility and resizes the window.
	// The app starts with the log collapsed (Лог ▸).
	t.logHeader = widget.NewButton("Лог ▸", onToggleLog)
	t.logHeader.Importance = widget.LowImportance // subtle, header-like

	t.logLabel = widget.NewLabel("")
	t.logLabel.Wrapping = fyne.TextWrapWord
	t.logScroll = container.NewVScroll(t.logLabel)
	t.logScroll.SetMinSize(fyne.NewSize(0, 200))

	// --- Control buttons ---
	t.startBtn = widget.NewButton("▶ Старт", func() {
		if t.svc.IsActive() {
			_ = t.svc.Restart()
			t.refreshStatus()
			return
		}
		if !t.svc.HasActiveConfig() {
			dialog.ShowInformation(
				fmt.Sprintf("%s: не выбран конфиг", t.svc.Label),
				"Активный конфиг не найден.\n\nСначала выберите профиль в выпадающем списке справа.",
				t.win,
			)
			return
		}
		_ = t.svc.Start()
		t.refreshStatus()
	})

	stopBtn := widget.NewButton("⏹ Стоп", func() {
		_ = t.svc.Stop()
		t.refreshStatus()
	})

	settingsBtn := widget.NewButton("⚙ Настройки", onSettings)

	leftBtns := container.NewHBox(t.startBtn, stopBtn, settingsBtn)

	// Right side: inline config dropdown (only for services with profiles).
	var rightSide fyne.CanvasObject
	if svc.HasProfiles {
		t.buildConfigSelect()
		cfgLabel := widget.NewLabel("Конфиг")
		rightSide = container.NewHBox(cfgLabel, t.configSelect)
	}

	// Button row: left buttons, spacer pushes config dropdown to the right.
	if rightSide != nil {
		t.buttonsRow = container.NewHBox(leftBtns, layout.NewSpacer(), rightSide)
	} else {
		t.buttonsRow = container.NewHBox(leftBtns, layout.NewSpacer())
	}

	// Build the initial content (log collapsed).  buildContent is also
	// called on every toggle — when expanded, a border layout puts the
	// scroll in the center; when collapsed, a plain VBox stacks status +
	// header + buttons with no empty gap.
	t.content = t.buildContent(false)

	// Initial refresh, then background loops.
	t.refreshStatus()
	t.refreshLog()
	go t.statusLoop()
	go t.logLoop()
	go t.networkLoop()

	return t
}

// buildConfigSelect creates the inline profile dropdown.  The callback
// applies the selected profile immediately — no separate Apply button.
func (t *serviceTab) buildConfigSelect() {
	profiles := t.svc.Profiles()
	t.configSelect = widget.NewSelect(profiles, func(selected string) {
		if t.suppressConfig {
			return
		}
		_ = t.svc.ApplyProfile(selected)
		t.refreshAll()
	})
	t.configSelect.PlaceHolder = "(не выбран)"

	// Pre-select the active profile without triggering the callback.
	t.suppressConfig = true
	if active := t.svc.ActiveProfileName(); active != "" {
		t.configSelect.SetSelected(active)
	}
	t.suppressConfig = false
}

// Content returns the root canvas object for this tab.
func (t *serviceTab) Content() fyne.CanvasObject { return t.content }

// buildContent constructs the tab's root layout.  When the log is
// expanded, a border layout puts the scroll in the center (filling the
// space between the header and the bottom buttons).  When collapsed,
// a plain VBox stacks status + header + buttons at the top with no
// empty gap — a border layout would pin the buttons to the bottom of
// the canvas, leaving a visible gap even at minimum window height.
func (t *serviceTab) buildContent(includeLog bool) fyne.CanvasObject {
	if includeLog {
		topWithHeader := container.NewVBox(t.topPanel, t.logHeader)
		return container.NewBorder(
			topWithHeader, // top: status + log header
			t.buttonsRow,  // bottom: control buttons + config dropdown
			nil, nil,
			t.logScroll,   // center: scrollable log
		)
	}
	// Collapsed: VBox — no center, no gap.
	return container.NewVBox(t.topPanel, t.logHeader, t.buttonsRow)
}

// setLogVisible rebuilds the tab content with or without the log scroll
// and updates the header chevron.  The header bar itself stays visible —
// it is the placeholder when collapsed.  The caller (ProxyApp.toggleLog)
// is responsible for updating the AppTabs item and resizing the window.
func (t *serviceTab) setLogVisible(visible bool) {
	t.content = t.buildContent(visible)
	t.content.Refresh()
	if visible {
		t.logHeader.SetText("Лог ▾")
	} else {
		t.logHeader.SetText("Лог ▸")
	}
}

// --- refresh functions (each safe to call from a goroutine) ----------------

// refreshStatus updates the status field widgets and the Start button text.
func (t *serviceTab) refreshStatus() {
	active := t.svc.IsActive()
	enabled := t.svc.IsEnabled()
	port := t.svc.Port()

	if active {
		t.stateDot.FillColor = color.NRGBA{R: 40, G: 180, B: 40, A: 255} // green
		t.stateText.SetText("Запущен")
	} else {
		t.stateDot.FillColor = color.NRGBA{R: 200, G: 40, B: 40, A: 255} // red
		t.stateText.SetText("Остановлен")
	}
	t.stateDot.Refresh()

	autoStr := "Выкл"
	if enabled {
		autoStr = "Вкл"
	}
	t.portVal.SetText(fmt.Sprintf("%d", port))
	t.autoVal.SetText(autoStr)

	if active {
		t.startBtn.SetText("♻ Рестарт")
	} else {
		t.startBtn.SetText("▶ Старт")
	}

	// Keep the config dropdown in sync.
	t.refreshConfigDropdown()
}

// refreshConfigDropdown syncs the dropdown options and selected item with
// the service's actual profile list and active profile.  Called from
// refreshStatus so newly-added profile files appear within ~2 s.
func (t *serviceTab) refreshConfigDropdown() {
	if !t.svc.HasProfiles || t.configSelect == nil {
		return
	}
	profiles := t.svc.Profiles()

	// Update the options list only if it actually changed (avoids flicker).
	changed := len(profiles) != len(t.configSelect.Options)
	if !changed {
		for i, p := range profiles {
			if t.configSelect.Options[i] != p {
				changed = true
				break
			}
		}
	}
	if changed {
		t.suppressConfig = true
		t.configSelect.Options = profiles
		t.configSelect.Refresh()
		t.suppressConfig = false
	}

	// Sync the selected item to the active profile (without re-applying).
	active := t.svc.ActiveProfileName()
	if active != "" && active != t.configSelect.Selected {
		t.suppressConfig = true
		t.configSelect.SetSelected(active)
		t.suppressConfig = false
	}
}

// refreshLog reads the latest log lines and scrolls to the bottom.
func (t *serviceTab) refreshLog() {
	text := t.svc.RecentLog(200)
	if text == "" {
		text = "(лог пуст — сервис работает, но не выводит сообщения)"
	}
	t.logLabel.SetText(text)
	t.logScroll.ScrollToBottom()
}

// refreshNetwork runs the connectivity check in a goroutine (it involves
// a curl call with up to 8 s timeout) and updates the label when done.
func (t *serviceTab) refreshNetwork() {
	t.netMu.Lock()
	if t.netBusy {
		t.netMu.Unlock()
		return
	}
	t.netBusy = true
	t.netMu.Unlock()

	go func() {
		defer func() {
			t.netMu.Lock()
			t.netBusy = false
			t.netMu.Unlock()
		}()

		active := t.svc.IsActive()
		port := t.svc.Port()
		if !active {
			t.network.SetText(fmt.Sprintf("⏸ Сервис остановлен (порт :%d)", port))
			return
		}
		t.network.SetText(CheckNetwork(port))
	}()
}

// --- background loops ------------------------------------------------------

func (t *serviceTab) statusLoop() {
	tk := time.NewTicker(2 * time.Second)
	defer tk.Stop()
	for range tk.C {
		t.refreshStatus()
	}
}

func (t *serviceTab) logLoop() {
	tk := time.NewTicker(3 * time.Second)
	defer tk.Stop()
	for range tk.C {
		t.refreshLog()
	}
}

func (t *serviceTab) networkLoop() {
	// Initial check after a short delay (let the service settle).
	time.Sleep(1 * time.Second)
	t.refreshNetwork()
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
	for range tk.C {
		t.refreshNetwork()
	}
}

// refreshAll forces an immediate refresh of status, log, and network.
// Called after settings changes or profile selection.
func (t *serviceTab) refreshAll() {
	t.refreshStatus()
	t.refreshLog()
	t.refreshNetwork()
}
