# ProxyControl

A lightweight GUI manager for [WireProxy](https://github.com/pufferffish/wireproxy) and [ByeDPI](https://github.com/hufrea/byedpi) proxy services, built with [Go](https://go.dev/) + [Fyne](https://fyne.io/).

Single binary, no runtime dependencies on Python/Node/Electron. Controls systemd user services, tails logs, checks connectivity, and manages config — all from a tabbed window.

## Features

- **Tabbed UI** — switch between WireProxy and ByeDPI at the top of the window (the thing YAD couldn't do).
- **Per-service status** — live indicator (🟢 running / 🔴 stopped), port, autostart state.
- **Log viewer** — tails `journalctl --user -u <service>` (or the log file if the unit appends to one), auto-scrolling, refreshed every 3 s.
- **Control buttons** — Start / Restart / Stop / Settings, in a horizontal row.
- **Network check** — verifies SOCKS5 connectivity by running `curl --socks5-hostname` through the service port every 10 s.
- **Settings dialog** — checkboxes for autostart (enable/disable systemd units), port entries (1–65535, validated), and "open folder" buttons for each service's config directory.
- **Persistent state** — remembers the last-opened tab and port settings across restarts.

## Screenshots

![ProxyControl main window](docs/screenshot.png)

## Prerequisites

### Runtime (on the target machine)

- Linux desktop with X11 or Wayland
- `systemctl --user` (systemd user session)
- `curl` (for network connectivity checks)
- `xdg-open` (for "open folder" buttons)
- `dbus-monitor` (for the wake-from-sleep resume monitor; optional — if absent the resume feature is skipped cleanly)
- WireProxy and/or ByeDPI binaries at `/usr/local/bin/wireproxy` and `/usr/local/bin/byedpi`

### Build (on the development machine)

#### 1. Go toolchain (≥ 1.23)

```bash
# Option A: official tarball (no root needed)
curl -sL https://go.dev/dl/go1.23.4.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH="/usr/local/go/bin:$PATH"

# Option B: package manager (Debian/Ubuntu)
sudo apt install golang-go
```

Verify:

```bash
go version  # should print go1.23.x
```

#### 2. CGO build dependencies

Fyne uses GLFW + OpenGL via CGO, so you need a C compiler and the X11/GL development headers:

**Debian / Ubuntu:**

```bash
sudo apt install -y \
    gcc pkg-config \
    libx11-dev libxext-dev libxfixes-dev libxi-dev \
    libxrandr-dev libxinerama-dev libxcursor-dev libxxf86vm-dev \
    libgl-dev libegl-dev libgles-dev libglvnd-dev libglx-dev \
    libxkbcommon-dev libxkbcommon-x11-dev \
    libwayland-dev
```

**Fedora / RHEL:**

```bash
sudo dnf install -y \
    gcc pkg-config \
    libX11-devel libXext-devel libXfixes-devel libXi-devel \
    libXrandr-devel libXinerama-devel libXcursor-devel libXxf86vm-devel \
    mesa-libGL-devel mesa-libEGL-devel mesa-libGLES-devel \
    libxkbcommon-devel libxkbcommon-x11-devel \
    wayland-devel
```

**Arch Linux:**

```bash
sudo pacman -S --needed \
    base-devel pkgconf \
    libx11 libxext libxfixes libxi libxrandr libxinerama libxcursor libxxf86vm \
    mesa glu libegl libgles \
    libxkbcommon libxkbcommon-x11 wayland
```

Verify the GL and X11 pkg-config packages are found:

```bash
pkg-config --exists gl x11 xrandr xinerama xcursor xi xxf86vm xext && echo "All OK"
```

## Build

```bash
cd proxycontrol
go build -o proxycontrol .
```

This produces a single `proxycontrol` binary (~30 MB, dynamically linked to system GL/X11 libraries).

### Cross-compilation

Fyne requires CGO, so cross-compiling requires a C cross-toolchain. For same-architecture builds:

```bash
CGO_ENABLED=1 go build -o proxycontrol .
```

### Stripping the binary (optional, for smaller size)

```bash
go build -ldflags="-s -w" -o proxycontrol .
# Reduces size from ~30 MB to ~22 MB
```

## Install

Copy the binary to somewhere in your `$PATH`:

```bash
cp proxycontrol ~/.local/bin/
```

Or install directly:

```bash
go install ./...
```

## Usage

```bash
proxycontrol
```

The app reads and writes to `~/.config/proxycontrol/`:

```
~/.config/proxycontrol/
├── proxycontrol.conf          # JSON: {"wp_port": 8883, "bd_port": 8882}
├── .last_tab                  # "wireproxy" or "byedpi"
├── wireproxy/
│   ├── wireproxy.conf         # active WireProxy config
│   ├── wireproxy.template     # template (BindAddress is rewritten on port change)
│   └── wireproxy.log          # log file (if the unit uses StandardOutput=append:)
└── byedpi/
    ├── byedpi.txt             # active ByeDPI config
    ├── .port                  # port file (read by the systemd unit)
    └── byedpi.log             # log file
```

### systemd user units (auto-created on first launch)

On startup, ProxyControl checks whether the `wireproxy.service`, `byedpi.service`,
and `proxycontrol-resume.service` user units exist in `~/.config/systemd/user/`.
**Any that are missing are created automatically**, `systemctl --user daemon-reload`
is run, and the resume monitor is enabled and started. Units that already exist are
left untouched, so manual customisations are never overwritten.

> **If you have a hand-written `wireproxy.service` with different config paths**
> (for example `~/.config/wireproxy.conf` instead of the GUI's
> `~/.config/proxycontrol/wireproxy/wireproxy.conf`), delete it and relaunch
> ProxyControl to get a unit that matches the GUI's config and log layout.

The generated units use the GUI's config/log paths, truncate the log on each
(re)start, and restart on failure with a 5 s delay:

**`~/.config/systemd/user/wireproxy.service`:**
```ini
[Unit]
Description=WireProxy SOCKS5 tunnel (ProxyControl)
After=network.target

[Service]
Type=simple
ExecStartPre=-/bin/sh -c '> %h/.config/proxycontrol/wireproxy/wireproxy.log'
ExecStart=/usr/local/bin/wireproxy -c %h/.config/proxycontrol/wireproxy/wireproxy.conf
Restart=on-failure
RestartSec=5s
StandardOutput=file:%h/.config/proxycontrol/wireproxy/wireproxy.log
StandardError=file:%h/.config/proxycontrol/wireproxy/wireproxy.log

[Install]
WantedBy=default.target
```

**`~/.config/systemd/user/byedpi.service`:**
```ini
[Unit]
Description=ByeDPI SOCKS5 proxy (ProxyControl)
After=network.target

[Service]
Type=simple
ExecStartPre=-/bin/sh -c '> %h/.config/proxycontrol/byedpi/byedpi.log'
ExecStart=/bin/bash -c '/usr/local/bin/byedpi -i 127.0.0.1 -p $(cat %h/.config/proxycontrol/byedpi/.port 2>/dev/null || echo 8882) $(cat %h/.config/proxycontrol/byedpi/.active_profile 2>/dev/null)'
Restart=on-failure
RestartSec=5s
StandardOutput=file:%h/.config/proxycontrol/byedpi/byedpi.log
StandardError=file:%h/.config/proxycontrol/byedpi/byedpi.log

[Install]
WantedBy=default.target
```

The byedpi unit reads the port from `.port` and the profile args from
`.active_profile`; both are written by the GUI when you change the port or pick
a profile.

### Restart on wake-from-sleep

WireProxy cannot reconnect after the computer suspends until the process is
restarted. ProxyControl installs a small resume monitor that fixes this:

- **`~/.config/proxycontrol/proxycontrol-resume.sh`** — a shell helper that
  subscribes to the `org.freedesktop.login1.Manager.PrepareForSleep` D-Bus
  signal. When the argument is `false` (the system is waking up), it restarts
  whichever proxy services were running before suspend. A service you had
  deliberately stopped stays stopped.
- **`~/.config/systemd/user/proxycontrol-resume.service`** — runs the helper
  as a long-lived user service (`Restart=always`, `RestartSec=10s`).
  `ConditionPathExists=/usr/bin/dbus-monitor` skips the start cleanly on
  systems without `dbus-monitor`, so there is no failure loop.

The resume monitor is enabled and started on every ProxyControl launch, so it
is active even if you never open the GUI after the first run. You can inspect
it with:

```bash
systemctl --user status proxycontrol-resume.service
```

### Port changes

When you change a port in Settings and click Apply:

- **WireProxy**: the `BindAddress = 127.0.0.1:<port>` line is rewritten in both `wireproxy.template` and `wireproxy.conf`, then the service is restarted if it was running.
- **ByeDPI**: the `.port` file is updated, then the service is restarted if it was running.

### Autostart

The Settings dialog checkboxes enable/disable the systemd user units (`systemctl --user enable/disable`). This controls whether the proxy starts automatically on login.

## Project structure

```
proxycontrol/
├── go.mod          # module definition (github.com/poiNt3D/ProxyControl)
├── main.go         # app entry, window, tabs, settings callback
├── config.go       # JSON config load/save, last-tab persistence
├── service.go      # service definitions, systemd wrappers, log reading, port management
├── systemd.go      # auto-create systemd user units + wake-from-sleep resume monitor
├── network.go      # SOCKS5 connectivity checker (curl)
├── tab.go          # per-service tab UI (status, log, buttons, refresh goroutines)
├── settings.go     # settings dialog (checkboxes, port entries, folder buttons)
├── .gitignore
└── README.md
```

## License

MIT
