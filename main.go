package main

import (
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

	a := app.New()
	a.SetIcon(nil) // TODO: bundle an icon

	w := a.NewWindow("ProxyControl")
	w.Resize(fyne.NewSize(720, 480))

	pa := &ProxyApp{cfg: cfg, win: w}

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

	h := float32(logExpandedHeight)
	if pa.logCollapsed {
		h = logCollapsedHeight
	}
	w := pa.win.Canvas().Size().Width
	pa.win.Resize(fyne.NewSize(w, h))
}
