package main

import (
	"fmt"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// ProxyApp holds the top-level application state shared across tabs.
type ProxyApp struct {
	cfg          *Config
	win          fyne.Window
	tabs         *container.AppTabs
	cmTab        *combinedTab
	wpTab        *serviceTab
	bdTab        *serviceTab
	ndTab        *serviceTab
	cm           *CombinedMode
	logCollapsed bool
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--test-install" {
		testInstaller()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--test-profiles" {
		testProfiles()
		return
	}

	cfg := LoadConfig()

	// Detect system language on first launch, write the built-in
	// language files to disk, and load the active string table.  Must
	// happen before any UI is built so all T() calls resolve correctly.
	InitLang(cfg)

	// Make sure the systemd user units exist (wireproxy, byedpi, and the
	// resume monitor).  Creates any that are missing, daemon-reloads, and
	// enables+starts the resume monitor.  Idempotent — safe every launch.
	EnsureSystemdUnits(cfg)

	a := app.New()
	a.SetIcon(nil) // TODO: bundle an icon

	w := a.NewWindow(T("app.title"))
	w.Resize(fyne.NewSize(720, logCollapsedHeight)) // start collapsed

	pa := &ProxyApp{cfg: cfg, win: w, logCollapsed: true}

	// Build all three service tabs.
	wpSvc := NewService("wireproxy", cfg)
	bdSvc := NewService("byedpi", cfg)
	ndSvc := NewService("nodpi", cfg)
	services := map[string]*ServiceDef{
		"wireproxy": wpSvc,
		"byedpi":    bdSvc,
		"nodpi":     ndSvc,
	}

	pa.wpTab = newServiceTab(wpSvc, cfg, w, pa.showSettings, pa.toggleLog, pa.rebuildTab)
	pa.bdTab = newServiceTab(bdSvc, cfg, w, pa.showSettings, pa.toggleLog, pa.rebuildTab)
	pa.ndTab = newServiceTab(ndSvc, cfg, w, pa.showSettings, pa.toggleLog, pa.rebuildTab)

	// Combined (Общий) mode: SOCKS5 listener on :8880 that chains to the
	// selected backend.  Built before the tabs so the first tab item is
	// the combined radio control.
	pa.cm = &CombinedMode{}
	pa.cmTab = newCombinedTab(pa.cm, services, w, cfg)
	go pa.cmTab.statusLoop()

	pa.tabs = container.NewAppTabs(
		container.NewTabItem(T("tab.combined"), pa.cmTab.Content()),
		container.NewTabItem(T("tab.wireproxy"), pa.wpTab.Content()),
		container.NewTabItem(T("tab.byedpi"), pa.bdTab.Content()),
		container.NewTabItem(T("tab.nodpi"), pa.ndTab.Content()),
	)
	pa.tabs.SetTabLocation(container.TabLocationTop)

	// Restore the last-selected tab.  Combined is index 0; the three
	// service tabs follow at 1/2/3.
	switch LoadLastTab() {
	case "wireproxy":
		pa.tabs.SelectIndex(1)
	case "byedpi":
		pa.tabs.SelectIndex(2)
	case "nodpi":
		pa.tabs.SelectIndex(3)
	default: // "combined"
		pa.tabs.SelectIndex(0)
	}

	// Persist tab selection on change.
	pa.tabs.OnSelected = func(tab *container.TabItem) {
		switch tab.Text {
		case T("tab.combined"):
			SaveLastTab("combined")
		case T("tab.wireproxy"):
			SaveLastTab("wireproxy")
		case T("tab.byedpi"):
			SaveLastTab("byedpi")
		case T("tab.nodpi"):
			SaveLastTab("nodpi")
		}
	}

	w.SetContent(pa.tabs)
	w.SetOnClosed(func() {
		pa.cm.Stop() // close the combined listener on exit
		a.Quit()
	})

	// Restore the combined-mode backend selection shortly after the
	// window opens, so any start-up error dialog is visible.
	if saved := LoadCombinedBackend(); saved != "" && saved != "off" {
		time.AfterFunc(1500*time.Millisecond, func() {
			pa.cmTab.Restore(saved)
		})
	}

	// Check for binary updates on startup if the user opted in.
	go pa.checkUpdatesOnStartup()

	// --show-settings: open the settings dialog 1s after launch (for
	// screenshots / automated testing).
	if len(os.Args) > 1 && os.Args[1] == "--show-settings" {
		time.AfterFunc(1*time.Second, func() {
			w.Resize(fyne.NewSize(560, 520))
			time.AfterFunc(300*time.Millisecond, func() {
				pa.showSettings()
			})
		})
	}

	w.ShowAndRun()
}

// showSettings opens the settings dialog.  After the user applies changes,
// both tabs are refreshed so the new ports / autostart state is visible.
func (pa *ProxyApp) showSettings() {
	showSettingsDialog(pa.win, pa.cfg, func() {
		pa.wpTab.refreshAll()
		pa.bdTab.refreshAll()
		pa.ndTab.refreshAll()
	})
}

// rebuildTab is called by a service tab after it swaps its content
// (e.g. after installing a binary and switching from install mode to
// normal mode).  It refreshes the AppTabs items so the new content is
// displayed.  The combined tab (index 0) is never rebuilt here.
func (pa *ProxyApp) rebuildTab() {
	pa.tabs.Items[1].Content = pa.wpTab.Content()
	pa.tabs.Items[2].Content = pa.bdTab.Content()
	pa.tabs.Items[3].Content = pa.ndTab.Content()
	pa.tabs.Refresh()
	pa.win.Content().Refresh()
}

// --- startup binary update check ------------------------------------------

// updateInfo holds the result of comparing one service's installed
// binary version with the latest GitHub release.
type updateInfo struct {
	tab     *serviceTab
	current string
	latest  string
}

// checkUpdatesOnStartup runs in a goroutine after the window opens.  If
// the user opted in (cfg.CheckUpdates), it checks each installed binary
// against the latest release from its configured fork and shows a dialog
// listing any that are out of date.
func (pa *ProxyApp) checkUpdatesOnStartup() {
	if !pa.cfg.CheckUpdates {
		return
	}
	// Let the window finish rendering before the network calls start.
	time.Sleep(2 * time.Second)

	var updates []updateInfo
	for _, tab := range []*serviceTab{pa.wpTab, pa.bdTab, pa.ndTab} {
		if !tab.svc.BinaryExists() {
			continue
		}
		current, latest, available, err := tab.svc.CheckForUpdate()
		if err != nil || !available {
			continue
		}
		updates = append(updates, updateInfo{tab: tab, current: current, latest: latest})
	}
	if len(updates) > 0 {
		pa.showUpdateDialog(updates)
	}
}

// showUpdateDialog presents a modal listing each service that has a
// newer release available.  Each row has a "Скачать" button that
// triggers the update flow (download + install over the existing
// binary) and dismisses the dialog.
func (pa *ProxyApp) showUpdateDialog(updates []updateInfo) {
	title := widget.NewLabel(T("update.available"))
	title.TextStyle = fyne.TextStyle{Bold: true}

	// Declare dlg up front so the button closures can capture it.
	var dlg *dialog.CustomDialog

	var rows []fyne.CanvasObject
	rows = append(rows, title)
	for _, u := range updates {
		info := widget.NewLabel(T("update.info", u.tab.svc.Label, u.current, u.latest))

		// Capture u for the closure.
		uu := u
		dlBtn := widget.NewButton(T("btn.download"), func() {
			dlg.Hide()
			uu.tab.onUpdateBinary()
		})
		rows = append(rows, container.NewHBox(info, dlBtn))
	}

	closeBtn := widget.NewButton(T("btn.close"), func() { dlg.Hide() })
	rows = append(rows, closeBtn)

	content := container.NewVBox(rows...)

	dlg = dialog.NewCustomWithoutButtons(T("update.title"), content, pa.win)
	dlg.Show()
}

// toggleLog collapses or expands the log panel on both tabs and resizes
// the window so it physically shrinks when the log is hidden.  The state
// is shared across tabs so both always agree.
func (pa *ProxyApp) toggleLog() {
	pa.logCollapsed = !pa.logCollapsed
	visible := !pa.logCollapsed
	pa.wpTab.setLogVisible(visible)
	pa.bdTab.setLogVisible(visible)
	pa.ndTab.setLogVisible(visible)

	// The tab content was rebuilt — update the AppTabs items so the
	// new content (with or without the scroll) is displayed.  The
	// combined tab (index 0) has no log and is not rebuilt.
	pa.tabs.Items[1].Content = pa.wpTab.Content()
	pa.tabs.Items[2].Content = pa.bdTab.Content()
	pa.tabs.Items[3].Content = pa.ndTab.Content()
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

// testInstaller exercises the GitHub download + extract + install flow
// without starting the GUI.  Triggered by `proxycontrol --test-install`.
// It tests both wireproxy (artem-russkikh/wireproxy-awg fork) and byedpi
// (hufrea/byedpi, whose archive contains ciadpi-<arch> not byedpi), plus
// the version-comparison update check.
func testInstaller() {
	cfg := LoadConfig()

	for _, key := range []string{"wireproxy", "byedpi", "nodpi"} {
		fmt.Printf("\n========== %s ==========\n", key)
		svc := NewService(key, cfg)
		fmt.Println("repo:", svc.RepoSlug(), "archive binary:", svc.ArchiveBinaryName())

		rel, err := svc.FetchLatestRelease()
		if err != nil {
			fmt.Println("FAIL FetchLatestRelease:", err)
			continue
		}
		fmt.Println("latest tag:", rel.TagName)

		tmp, err := svc.DownloadAndExtract(rel, func(done, total int64) {
			if total > 0 && done%100000 == 0 {
				fmt.Printf("  progress: %d/%d (%.0f%%)\n", done, total, float64(done)/float64(total)*100)
			}
		})
		if err != nil {
			fmt.Println("FAIL DownloadAndExtract:", err)
			continue
		}
		fmt.Println("extracted:", tmp)
		if info, e := os.Stat(tmp); e == nil {
			fmt.Println("  size:", info.Size(), "executable:", info.Mode()&0o111 != 0)
		}

		destPath := UserBinDir() + "/" + svc.BinaryName()
		os.Remove(destPath)
		if err := InstallBinary(tmp, destPath, false); err != nil {
			fmt.Println("FAIL InstallBinary:", err)
			os.Remove(tmp)
			continue
		}
		os.Remove(tmp)
		fmt.Println("installed to:", destPath)

		// Point the config at the installed binary so version checks work.
		svc.SetBinaryPath(destPath)

		// Version check.
		v, err := svc.InstalledVersion()
		if err != nil {
			fmt.Println("FAIL InstalledVersion:", err)
		} else {
			fmt.Println("installed version:", v)
		}
		current, latest, available, err := svc.CheckForUpdate()
		if err != nil {
			fmt.Println("FAIL CheckForUpdate:", err)
		} else {
			fmt.Printf("CheckForUpdate: current=%s latest=%s updateAvailable=%v\n",
				current, latest, available)
		}

		os.Remove(destPath)
	}

	fmt.Println("\nDONE")
}

// testProfiles prints the config profiles found for each service, to
// verify that .conf files are now listed for byedpi (not just .txt).
// Triggered by `proxycontrol --test-profiles`.
func testProfiles() {
	cfg := LoadConfig()
	for _, key := range []string{"wireproxy", "byedpi", "nodpi"} {
		svc := NewService(key, cfg)
		exts := svc.configExts()
		profiles := svc.Profiles()
		fmt.Printf("%s: exts=%v profiles=%v\n", svc.Label, exts, profiles)
	}
}
