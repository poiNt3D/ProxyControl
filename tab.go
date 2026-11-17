package main

import (
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// serviceTab holds the UI widgets for one service tab and runs the
// background refresh goroutines that keep them up to date.
type serviceTab struct {
	svc       *ServiceDef
	cfg       *Config
	win       fyne.Window
	onSettings func()

	status   *widget.Label
	network  *widget.Label
	logLabel *widget.Label
	logScroll *container.Scroll
	startBtn *widget.Button

	content fyne.CanvasObject

	// networkMu prevents overlapping network checks.
	netMu   sync.Mutex
	netBusy bool
}

// newServiceTab creates the tab UI and starts its refresh goroutines.
func newServiceTab(svc *ServiceDef, cfg *Config, win fyne.Window, onSettings func()) *serviceTab {
	t := &serviceTab{
		svc:       svc,
		cfg:       cfg,
		win:       win,
		onSettings: onSettings,
	}

	t.status = widget.NewLabel("")
	t.status.TextStyle = fyne.TextStyle{Bold: true}

	t.network = widget.NewLabel("")

	t.logLabel = widget.NewLabel("")
	t.logLabel.Wrapping = fyne.TextWrapWord
	t.logScroll = container.NewVScroll(t.logLabel)
	t.logScroll.SetMinSize(fyne.NewSize(0, 200))

	// Combined Start/Restart button: label changes based on state.
	// Before starting, checks that a config profile has been selected
	// (same guard as action_start_restart in the bash version).
	t.startBtn = widget.NewButton("▶ Старт", func() {
		if t.svc.IsActive() {
			_ = t.svc.Restart()
			t.refreshStatus()
			return
		}
		if !t.svc.HasActiveConfig() {
			dialog.ShowInformation(
				fmt.Sprintf("%s: не выбран конфиг", t.svc.Label),
				"Активный конфиг не найден.\n\nСначала выберите профиль кнопкой «Конфиг».",
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

	// Build the button row.  "Select Config" is only shown for services
	// that support profile selection (HasProfiles).
	var btnObjs []fyne.CanvasObject
	btnObjs = append(btnObjs, t.startBtn, stopBtn)
	if t.svc.HasProfiles {
		configBtn := widget.NewButton("📋 Конфиг", t.showConfigDialog)
		btnObjs = append(btnObjs, configBtn)
	}
	btnObjs = append(btnObjs, settingsBtn)

	buttons := container.NewHBox(btnObjs...)
	topPanel := container.NewVBox(t.status, t.network)

	t.content = container.NewBorder(
		topPanel,    // top:  status + network
		buttons,     // bottom: control buttons
		nil, nil,    // no left/right sidebars
		t.logScroll, // center: scrollable log
	)

	// Initial refresh, then background loops.
	t.refreshStatus()
	t.refreshLog()
	go t.statusLoop()
	go t.logLoop()
	go t.networkLoop()

	return t
}

// Content returns the root canvas object for this tab.
func (t *serviceTab) Content() fyne.CanvasObject { return t.content }

// --- refresh functions (each safe to call from a goroutine) ----------------

// refreshStatus updates the status label and the Start/Stop button text.
func (t *serviceTab) refreshStatus() {
	active := t.svc.IsActive()
	enabled := t.svc.IsEnabled()
	port := t.svc.Port()

	var dot, stateStr string
	if active {
		dot = "🟢"
		stateStr = "Запущен"
	} else {
		dot = "🔴"
		stateStr = "Остановлен"
	}
	autoStr := "Выкл"
	if enabled {
		autoStr = "Вкл"
	}

	t.status.SetText(fmt.Sprintf("%s %s: %s  |  Порт: %d  |  Авто: %s",
		dot, t.svc.Label, stateStr, port, autoStr))

	// Append active profile name if the service supports profiles.
	if t.svc.HasProfiles {
		profile := t.svc.ActiveProfileName()
		if profile == "" {
			profile = "(не выбран)"
		}
		t.status.SetText(t.status.Text + "  |  Конфиг: " + profile)
	}

	if active {
		t.startBtn.SetText("♻ Рестарт")
	} else {
		t.startBtn.SetText("▶ Старт")
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
// Called after settings changes.
func (t *serviceTab) refreshAll() {
	t.refreshStatus()
	t.refreshLog()
	t.refreshNetwork()
}

// showConfigDialog opens a modal that lists available config profiles in a
// dropdown.  The currently active profile is pre-selected.  On "Применить"
// the chosen profile is applied (copied to the active config / .active_profile)
// and the service is restarted if it was running.
func (t *serviceTab) showConfigDialog() {
	profiles := t.svc.Profiles()
	if len(profiles) == 0 {
		dialog.ShowInformation(
			"Нет конфигураций",
			fmt.Sprintf("В папке\n%s\nнет файлов *.%s.",
				t.svc.ConfigDir, t.svc.ConfExt),
			t.win,
		)
		return
	}

	active := t.svc.ActiveProfileName()
	sel := widget.NewSelect(profiles, nil)
	if active != "" {
		sel.SetSelected(active)
	} else {
		sel.SetSelected(profiles[0])
	}

	items := []*widget.FormItem{
		{Text: "Профиль", Widget: sel},
	}

	d := dialog.NewForm(
		fmt.Sprintf("Выбор конфигурации — %s", t.svc.Label),
		"Применить", "Отмена",
		items,
		func(confirmed bool) {
			if !confirmed || sel.Selected == "" {
				return
			}
			_ = t.svc.ApplyProfile(sel.Selected)
			t.refreshAll()
		},
		t.win,
	)
	d.Resize(fyne.NewSize(420, 200))
	d.Show()
}
