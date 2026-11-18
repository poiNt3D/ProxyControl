package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	if len(os.Args) > 1 && os.Args[1] == "--test-install" {
		testInstaller()
		return
	}

	cfg := LoadConfig()

	// Make sure the systemd user units exist (wireproxy, byedpi, and the
	// resume monitor).  Creates any that are missing, daemon-reloads, and
	// enables+starts the resume monitor.  Idempotent — safe every launch.
	EnsureSystemdUnits(cfg)

	a := app.New()
	a.SetIcon(nil) // TODO: bundle an icon

	w := a.NewWindow("ProxyControl")
	w.Resize(fyne.NewSize(720, logCollapsedHeight)) // start collapsed

	pa := &ProxyApp{cfg: cfg, win: w, logCollapsed: true}

	// Build both service tabs.
	wpSvc := NewService("wireproxy", cfg)
	bdSvc := NewService("byedpi", cfg)

	pa.wpTab = newServiceTab(wpSvc, cfg, w, pa.showSettings, pa.toggleLog, pa.rebuildTab)
	pa.bdTab = newServiceTab(bdSvc, cfg, w, pa.showSettings, pa.toggleLog, pa.rebuildTab)

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

// rebuildTab is called by a service tab after it swaps its content
// (e.g. after installing a binary and switching from install mode to
// normal mode).  It refreshes the AppTabs items so the new content is
// displayed.
func (pa *ProxyApp) rebuildTab() {
	pa.tabs.Items[0].Content = pa.wpTab.Content()
	pa.tabs.Items[1].Content = pa.bdTab.Content()
	pa.tabs.Refresh()
	pa.win.Content().Refresh()
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

// testInstaller exercises the GitHub download + extract + install flow
// without starting the GUI.  Triggered by `proxycontrol --test-install`.
// It downloads wireproxy to ~/.local/bin and prints each step's result.
func testInstaller() {
	cfg := LoadConfig()
	svc := NewService("wireproxy", cfg)

	fmt.Println("=== 1. FetchLatestRelease ===")
	rel, err := svc.FetchLatestRelease()
	if err != nil {
		fmt.Println("FAIL:", err)
		return
	}
	fmt.Println("  tag:", rel.TagName, "assets:", len(rel.Assets))

	fmt.Println("=== 2. MatchAsset ===")
	asset, err := svc.MatchAsset(rel)
	if err != nil {
		fmt.Println("FAIL:", err)
		return
	}
	fmt.Println("  matched:", asset.Name, "size:", asset.Size)

	fmt.Println("=== 3. DownloadAndExtract ===")
	tmp, err := svc.DownloadAndExtract(rel, func(done, total int64) {
		if total > 0 && done%100000 == 0 {
			fmt.Printf("  progress: %d/%d (%.0f%%)\n", done, total, float64(done)/float64(total)*100)
		}
	})
	if err != nil {
		fmt.Println("FAIL:", err)
		return
	}
	defer os.Remove(tmp)
	fmt.Println("  extracted to:", tmp)
	if info, e := os.Stat(tmp); e == nil {
		fmt.Println("  size:", info.Size(), "executable:", info.Mode()&0o111 != 0)
	}

	fmt.Println("=== 4. InstallBinary (user bin) ===")
	destPath := UserBinDir() + "/" + svc.BinaryName()
	// Remove any previous install so the test is clean.
	os.Remove(destPath)
	if err := InstallBinary(tmp, destPath, false); err != nil {
		fmt.Println("FAIL:", err)
		return
	}
	fmt.Println("  installed to:", destPath)
	if info, e := os.Stat(destPath); e == nil {
		fmt.Println("  size:", info.Size(), "executable:", info.Mode()&0o111 != 0)
	} else {
		fmt.Println("FAIL: installed file not found")
		return
	}

	fmt.Println("=== 5. Verify it runs ===")
	out, err := exec.Command(destPath, "--version").CombinedOutput()
	if err != nil {
		fmt.Println("  (version check failed — expected for some binaries, err:", err, ")")
	} else {
		fmt.Println("  output:", strings.TrimSpace(string(out)))
	}

	fmt.Println("=== 6. FinalizeInstall (config + unit) ===")
	svc.FinalizeInstall(destPath)
	fmt.Println("  config wp_path:", cfg.WPPath)

	// Clean up the test install.
	os.Remove(destPath)
	fmt.Println("\nALL STEPS PASSED")
}
