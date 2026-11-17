package main

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
)

// ProxyApp holds the top-level application state shared across tabs.
type ProxyApp struct {
	cfg          *Config
	win          fyne.Window
	tabs         *container.AppTabs
	wpTab        *serviceTab
	bdTab        *serviceTab
	logCollapsed bool
}

func main() {
	cfg := LoadConfig()

	// Make sure the systemd user units exist (wireproxy, byedpi, and the
	// resume monitor).  Creates any that are missing, daemon-reloads, and
	// enables+starts the resume monitor.  Idempotent — safe every launch.
	EnsureSystemdUnits()

	a := app.New()
	a.SetIcon(nil) // TODO: bundle an icon

	w := a.NewWindow("ProxyControl")
	w.Resize(fyne.NewSize(720, logCollapsedHeight)) // start collapsed

	pa := &ProxyApp{cfg: cfg, win: w, logCollapsed: true}

	// Build both service tabs.
	wpSvc := NewService("wireproxy", cfg)
	bdSvc := NewService("byedpi", cfg)

	pa.wpTab = newServiceTab(wpSvc, cfg, w, pa.showSettings, pa.toggleLog)
	pa.bdTab = newServiceTab(bdSvc, cfg, w, pa.showSettings, pa.toggleLog)

	pa.tabs = container.NewAppTabs(
		container.NewTabItem("WireProxy", pa.wpTab.Content()),
		container.NewTabItem("ByeDPI", pa.bdTab.Content()),
	)
	pa.tabs.SetTabLocation(container.TabLocationTop)

	// Restore the last-selected tab.
	if LoadLastTab() == "byedpi" {
		pa.tabs.SelectIndex(1)
	}

	// Persist tab selection on change.
	pa.tabs.OnSelected = func(tab *container.TabItem) {
		switch tab.Text {
		case "WireProxy":
			SaveLastTab("wireproxy")
		case "ByeDPI":
			SaveLastTab("byedpi")
		}
	}

	w.SetContent(pa.tabs)
	w.SetOnClosed(func() { a.Quit() })

	w.ShowAndRun()
}

// showSettings opens the settings dialog.  After the user applies changes,
// both tabs are refreshed so the new ports / autostart state is visible.
func (pa *ProxyApp) showSettings() {
	showSettingsDialog(pa.win, pa.cfg, func() {
		pa.wpTab.refreshAll()
		pa.bdTab.refreshAll()
	})
}

// toggleLog collapses or expands the log panel on both tabs and resizes
// the window so it physically shrinks when the log is hidden.  The state
// is shared across tabs so both always agree.
func (pa *ProxyApp) toggleLog() {
	pa.logCollapsed = !pa.logCollapsed
	visible := !pa.logCollapsed
	pa.wpTab.setLogVisible(visible)
	pa.bdTab.setLogVisible(visible)

	// The tab content was rebuilt — update the AppTabs items so the
	// new content (with or without the scroll) is displayed.
	pa.tabs.Items[0].Content = pa.wpTab.Content()
	pa.tabs.Items[1].Content = pa.bdTab.Content()
	pa.tabs.Refresh()
	// Force the content to drop any cached layout from the previous
	// (expanded) state before we measure its minimum, otherwise MinSize
	// can still report the stale ~480px expanded height.
	pa.win.Content().Refresh()

	w := pa.win.Canvas().Size().Width
	if pa.logCollapsed {
		// Measure the rebuilt VBox's true minimum height and request
		// exactly that, rather than a fixed constant (150) that relied on
		// Fyne clamping it up to the content min.  That clamping works at
		// startup (window created fresh) but is unreliable after a toggle:
		// the window manager's minimum-size hint can linger at the
		// previously-expanded height, so a too-small request is clamped
		// back up to the stale expanded value and the window stays too
		// tall.  Requesting the real minimum leaves nothing to clamp.
		h := pa.win.Content().MinSize().Height
		if h < logCollapsedHeight {
			h = logCollapsedHeight // safety floor
		}
		pa.win.Resize(fyne.NewSize(w, h))

		// Some window managers clamp the synchronous Resize above against
		// the stale expanded min-size hint, which Fyne only refreshes on
		// the next render tick (after Resize returns).  Re-issue the same
		// Resize one frame later so the hint has caught up and the window
		// actually shrinks.  Window.Resize marshals to the main thread via
		// runOnMain, so this is safe from the timer goroutine.  Re-check
		// logCollapsed inside in case the user toggled back to expanded
		// before the timer fired.
		target := h
		time.AfterFunc(80*time.Millisecond, func() {
			if pa.logCollapsed {
				pa.win.Resize(fyne.NewSize(w, target))
			}
		})
	} else {
		pa.win.Resize(fyne.NewSize(w, logExpandedHeight))
	}
}
