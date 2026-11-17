package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
)

// ProxyApp holds the top-level application state shared across tabs.
type ProxyApp struct {
	cfg    *Config
	win    fyne.Window
	tabs   *container.AppTabs
	wpTab  *serviceTab
	bdTab  *serviceTab
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

	pa.wpTab = newServiceTab(wpSvc, cfg, w, pa.showSettings)
	pa.bdTab = newServiceTab(bdSvc, cfg, w, pa.showSettings)

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
