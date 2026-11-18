package main

import (
	"fmt"
	"image/color"
	"os"
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
//
// A tab has two modes:
//   - normal: the binary exists at the configured path → status panel,
//     log, control buttons, config dropdown.
//   - install: the binary is missing → two centered buttons ("select
//     local binary" / "download from GitHub").  Background loops are
//     not started in this mode.
type serviceTab struct {
	svc         *ServiceDef
	cfg         *Config
	win         fyne.Window
	onSettings  func()
	onToggleLog func()
	onRebuild   func() // called after content is swapped (install → normal)

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

	loopsRunning bool // true once the background goroutines have been started

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

// newServiceTab creates the tab UI.  If the service's binary is missing
// the tab starts in install mode (two buttons); otherwise it starts in
// normal mode with the background refresh loops running.
func newServiceTab(svc *ServiceDef, cfg *Config, win fyne.Window,
	onSettings func(), onToggleLog func(), onRebuild func()) *serviceTab {

	t := &serviceTab{
		svc:         svc,
		cfg:         cfg,
		win:         win,
		onSettings:  onSettings,
		onToggleLog: onToggleLog,
		onRebuild:   onRebuild,
	}

	// Create the normal-mode widgets up front — they are cheap struct
	// allocations and are reused when the tab later switches from
	// install mode to normal mode after a binary is installed.
	t.initNormalWidgets()

	if svc.BinaryExists() {
		t.enterNormalMode(false)
	} else {
		t.content = t.buildInstallContent()
	}

	return t
}

// initNormalWidgets builds the status panel, log panel, control buttons
// and config dropdown.  It does NOT set t.content or start loops — that
// is done by enterNormalMode so the same widgets can be reused after an
// install.
func (t *serviceTab) initNormalWidgets() {
	// --- Status panel (structured fields, no pipe separators) ---
	titleLabel := widget.NewLabel(t.svc.Label)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Colored status indicator: a canvas.Circle in a fixed-size cell so
	// the HBox reserves space for it (canvas primitives have zero MinSize).
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
	t.logHeader = widget.NewButton("Лог ▸", t.onToggleLog)
	t.logHeader.Importance = widget.LowImportance

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

	settingsBtn := widget.NewButton("⚙ Настройки", t.onSettings)

	leftBtns := container.NewHBox(t.startBtn, stopBtn, settingsBtn)

	// Right side: inline config dropdown (only for services with profiles).
	var rightSide fyne.CanvasObject
	if t.svc.HasProfiles {
		t.buildConfigSelect()
		cfgLabel := widget.NewLabel("Конфиг")
		rightSide = container.NewHBox(cfgLabel, t.configSelect)
	}

	if rightSide != nil {
		t.buttonsRow = container.NewHBox(leftBtns, layout.NewSpacer(), rightSide)
	} else {
		t.buttonsRow = container.NewHBox(leftBtns, layout.NewSpacer())
	}
}

// enterNormalMode builds the normal (status + log + buttons) content and
// starts the background refresh loops if they are not already running.
// logVisible controls whether the log scroll is shown.
func (t *serviceTab) enterNormalMode(logVisible bool) {
	t.content = t.buildContent(logVisible)
	if !t.logVisibleState() && logVisible {
		t.logHeader.SetText("Лог ▾")
	} else {
		t.logHeader.SetText("Лог ▸")
	}
	t.refreshStatus()
	t.refreshLog()
	if !t.loopsRunning {
		t.loopsRunning = true
		go t.statusLoop()
		go t.logLoop()
		go t.networkLoop()
	}
}

// logVisibleState reports whether the log scroll is currently part of
// the content (expanded).  It inspects the content tree rather than a
// stored flag so it stays correct after rebuilds.
func (t *serviceTab) logVisibleState() bool {
	// After enterNormalMode/buildContent the header text tells us: "▾"
	// means expanded, "▸" means collapsed.
	return t.logHeader != nil && t.logHeader.Text == "Лог ▾"
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

	t.suppressConfig = true
	if active := t.svc.ActiveProfileName(); active != "" {
		t.configSelect.SetSelected(active)
	}
	t.suppressConfig = false
}

// Content returns the root canvas object for this tab.
func (t *serviceTab) Content() fyne.CanvasObject { return t.content }

// buildContent constructs the normal-mode root layout.  When the log is
// expanded, a border layout puts the scroll in the center (filling the
// space between the header and the bottom buttons).  When collapsed,
// a plain VBox stacks status + header + buttons at the top with no
// empty gap.
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
	return container.NewVBox(t.topPanel, t.logHeader, t.buttonsRow)
}

// buildInstallContent builds the two-button install UI shown when the
// binary is not found.  The layout is kept compact (title + path + a
// centered button row) so it fits in the collapsed window height.
func (t *serviceTab) buildInstallContent() fyne.CanvasObject {
	title := widget.NewLabel(fmt.Sprintf("⚠ %s: бинарный файл не найден", t.svc.Label))
	title.Alignment = fyne.TextAlignCenter
	title.TextStyle = fyne.TextStyle{Bold: true}

	path := widget.NewLabel(t.svc.BinaryPath())
	path.Alignment = fyne.TextAlignCenter

	selectBtn := widget.NewButton("📁 Выбрать файл", t.onSelectBinary)
	downloadBtn := widget.NewButton("⬇ Скачать с GitHub", t.onDownloadBinary)

	buttons := container.NewHBox(selectBtn, downloadBtn)
	centered := container.NewHBox(layout.NewSpacer(), buttons, layout.NewSpacer())

	return container.NewVBox(title, path, centered)
}

// --- install-mode button handlers -----------------------------------------

// onSelectBinary opens a file picker; the chosen path is saved to the
// config and the tab switches to normal mode.
func (t *serviceTab) onSelectBinary() {
	dlg := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		path := reader.URI().Path()
		reader.Close()

		// Validate it looks like a file (not a directory).
		if info, e := os.Stat(path); e != nil || info.IsDir() {
			dialog.ShowError(fmt.Errorf("выбранный путь не является файлом"), t.win)
			return
		}

		t.svc.SetBinaryPath(path)
		UpdateUnitBinaryPath(t.svc)
		t.rebuildForBinaryState()
	}, t.win)
	dlg.Show()
}

// onDownloadBinary fetches the latest GitHub release, downloads + extracts
// the binary, then asks where to install it.  A progress dialog is shown
// during the download.
func (t *serviceTab) onDownloadBinary() {
	progress := widget.NewProgressBar()
	progress.Min = 0
	progress.Max = 1
	statusLabel := widget.NewLabel("Получение информации о релизе…")
	statusLabel.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(statusLabel, progress)
	dlg := dialog.NewCustomWithoutButtons(
		fmt.Sprintf("Скачивание %s", t.svc.Label), content, t.win)
	dlg.Show()

	go func() {
		rel, err := t.svc.FetchLatestRelease()
		if err != nil {
			dlg.Hide()
			t.showError(err)
			return
		}
		statusLabel.SetText(fmt.Sprintf("Скачивание %s…", rel.TagName))
		tmp, err := t.svc.DownloadAndExtract(rel, func(done, total int64) {
			if total > 0 {
				progress.SetValue(float64(done) / float64(total))
			}
		})
		if err != nil {
			dlg.Hide()
			t.showError(err)
			return
		}
		dlg.Hide()
		t.askInstallLocation(tmp)
	}()
}

// askInstallLocation shows a dialog with two choices: user bin
// (~/.local/bin, no privilege needed) or system-wide (/usr/local/bin,
// pkexec/sudo).  After the choice, the binary is installed, the path is
// saved, the systemd unit is updated, and the tab rebuilds.
func (t *serviceTab) askInstallLocation(tmpBinary string) {
	userDir := UserBinDir()
	systemDir := SystemBinDir()

	userBtn := widget.NewButton(fmt.Sprintf("📂 %s (без sudo)", userDir), func() {
		t.doInstall(tmpBinary, false)
	})
	systemBtn := widget.NewButton(fmt.Sprintf("🔐 %s (sudo)", systemDir), func() {
		t.doInstall(tmpBinary, true)
	})

	msg := widget.NewLabel("Куда установить бинарный файл?")
	msg.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(msg, userBtn, systemBtn)
	dlg := dialog.NewCustomWithoutButtons("Установка", content, t.win)
	dlg.Show()
}

// doInstall copies the temp binary to the chosen destination, finalises
// the config + unit, and rebuilds the tab.  systemWide triggers the
// pkexec/sudo path.  A busy dialog is shown during the (potentially
// blocking) privileged copy.
func (t *serviceTab) doInstall(tmpBinary string, systemWide bool) {
	destDir := UserBinDir()
	if systemWide {
		destDir = SystemBinDir()
	}
	destPath := destDir + "/" + t.svc.BinaryName()

	dlg := dialog.NewCustomWithoutButtons(
		"Установка…", widget.NewLabel("Копирование файла…"), t.win)
	if systemWide {
		dlg.Show() // pkexec will block; show the busy dialog
	}

	go func() {
		err := InstallBinary(tmpBinary, destPath, systemWide)
		os.Remove(tmpBinary)
		if systemWide {
			dlg.Hide()
		}
		if err != nil {
			t.showError(err)
			return
		}
		t.svc.FinalizeInstall(destPath)
		t.rebuildForBinaryState()
	}()
}

// rebuildForBinaryState swaps the tab content between install and normal
// mode depending on whether the binary now exists, then notifies the
// parent app so the AppTabs item is refreshed.
func (t *serviceTab) rebuildForBinaryState() {
	if t.svc.BinaryExists() {
		t.enterNormalMode(false)
	} else {
		t.content = t.buildInstallContent()
	}
	if t.onRebuild != nil {
		t.onRebuild()
	}
}

// showError displays an error dialog (safe to call from a goroutine).
func (t *serviceTab) showError(err error) {
	dialog.ShowError(err, t.win)
}

// setLogVisible rebuilds the tab content with or without the log scroll
// and updates the header chevron.  In install mode (binary missing) this
// is a no-op — there is no log to toggle.
func (t *serviceTab) setLogVisible(visible bool) {
	if !t.svc.BinaryExists() {
		return
	}
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

	t.refreshConfigDropdown()
}

// refreshConfigDropdown syncs the dropdown options and selected item with
// the service's actual profile list and active profile.
func (t *serviceTab) refreshConfigDropdown() {
	if !t.svc.HasProfiles || t.configSelect == nil {
		return
	}
	profiles := t.svc.Profiles()

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

// refreshNetwork runs the connectivity check in a goroutine.
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
	time.Sleep(1 * time.Second)
	t.refreshNetwork()
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
	for range tk.C {
		t.refreshNetwork()
	}
}

// refreshAll forces an immediate refresh of status, log, and network.
func (t *serviceTab) refreshAll() {
	t.refreshStatus()
	t.refreshLog()
	t.refreshNetwork()
}
