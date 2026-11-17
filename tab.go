package main

import (
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// serviceTab holds the UI widgets for one service tab and runs the
// background refresh goroutines that keep them up to date.
type serviceTab struct {
	svc       *ServiceDef
	cfg       *Config
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
func newServiceTab(svc *ServiceDef, cfg *Config, onSettings func()) *serviceTab {
	t := &serviceTab{
		svc:       svc,
		cfg:       cfg,
		onSettings: onSettings,
	}

	t.status = widget.NewLabel("")
	t.status.TextStyle = fyne.TextStyle{Bold: true}

	t.network = widget.NewLabel("")

	t.logLabel = widget.NewLabel("")
	t.logLabel.Wrapping = fyne.TextWrapWord
	t.logScroll = container.NewVScroll(t.logLabel)
	t.logScroll.SetMinSize(fyne.NewSize(0, 200))

	t.startBtn = widget.NewButton("▶ Старт", func() {
		if t.svc.IsActive() {
			_ = t.svc.Restart()
		} else {
			_ = t.svc.Start()
		}
		t.refreshStatus()
	})

	restartBtn := widget.NewButton("♻ Рестарт", func() {
		_ = t.svc.Restart()
		t.refreshStatus()
	})

	stopBtn := widget.NewButton("⏹ Стоп", func() {
		_ = t.svc.Stop()
		t.refreshStatus()
	})

	settingsBtn := widget.NewButton("⚙ Настройки", onSettings)

	buttons := container.NewHBox(t.startBtn, restartBtn, stopBtn, settingsBtn)
	topPanel := container.NewVBox(t.status, t.network)

	t.content = container.NewBorder(
		topPanel,   // top:  status + network
		buttons,    // bottom: control buttons
		nil, nil,   // no left/right sidebars
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
