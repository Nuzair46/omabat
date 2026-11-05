# Omabat

[![CI](https://img.shields.io/github/actions/workflow/status/nuzair46/omabat/ci.yml?branch=main&style=flat-square&label=CI)](https://github.com/nuzair46/omabat/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/nuzair46/omabat?display_name=tag&sort=semver&style=flat-square)](https://github.com/nuzair46/omabat/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/nuzair46/omabat/total?style=flat-square)](https://github.com/nuzair46/omabat/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/nuzair46/omabat?style=flat-square)](go.mod)
[![License](https://img.shields.io/github/license/nuzair46/omabat?style=flat-square)](LICENSE)

Omabat is a lightweight Linux battery-history daemon and terminal dashboard. Use it to understand battery level changes, charging periods, suspend drain, power profiles, and battery health over time.

![Omabat history and health dashboard](assets/demo.png)

## Highlights

- Battery-level history for the last `24h`, `3d`, or `7d`
- AC, suspend, and power-profile timelines
- Average and peak discharge-power statistics
- Battery health, designed capacity, current full capacity, and vendor details
- Immediate live updates while the dashboard is open
- Optional background systemd user service
- Battery-history access from Omarchy 4's existing battery widget
- Optional Waybar tray indicator on older desktops
- Local SQLite storage with no account or network service

## Install

Install the latest release and start the background collector with one command:

```sh
curl -fsSL https://raw.githubusercontent.com/nuzair46/omabat/main/install.sh | sh
```

The installer detects AMD64 or ARM64, verifies the release checksum, installs Omabat to `~/.local/bin/omabat`, and runs `omabat install` for the current user. No root privileges are required.

After the daemon is installed, the script asks in English whether to integrate with your desktop panel, warning that this changes panel settings and may restart the panel. The prompt is `[Y/n]`: press Enter to accept, or enter `n` to skip. It reads from `/dev/tty`, so it also works with `curl … | sh`. Without an interactive terminal, integration is skipped. Declining leaves any existing integration unchanged, and an integration failure does not undo installation of the binary or daemon. You can run `omabat integrate` later.

The script requires a release that provides `omabat integrate`. It refuses to run the daemon setup of older releases, whose `install` command also changes panel settings without asking. Use `OMABAT_VERSION` to select a compatible release if needed.

Review [`install.sh`](install.sh) before running it, or download an archive manually from [GitHub Releases](https://github.com/nuzair46/omabat/releases/latest).

Omabat requires Linux with `/sys/class/power_supply`. For the intended icon rendering, use a terminal configured with a [Nerd Font](https://www.nerdfonts.com/).

UPower, systemd-logind, `powerprofilesctl`, and the desktop bar are optional integrations. Omabat falls back to sysfs when UPower is unavailable.

## Start Collecting History

The dashboard records an immediate sample and refreshes every three seconds while open. To keep collecting after closing it, install the user daemon:

```sh
omabat install
```

This only installs and starts `~/.config/systemd/user/omabat.service`; it does not modify or restart desktop panels, including on repeated installs. The daemon samples every 120 seconds and records readings immediately before suspend and after resume.

## Optional Desktop Integration

To integrate explicitly, independently of installing the daemon, run:

```sh
omabat integrate
```

This command applies integration directly, without a second confirmation prompt. On Omarchy 4, it adds a **Battery history** button to the existing battery popup. Middle-clicking the battery icon also opens Omabat. The usual left-click power panel, right-click percentage toggle, and power-profile controls remain available.

The integration uses `omarchy plugin clone omarchy.power` to make a user-owned copy under `~/.config/omarchy/plugins/<username>.power/`, or extends your already enabled power-widget clone. It backs up the original layout and panel as `.omabat.bak` files, preserves widget settings, and does not add a second battery icon. The shell restarts when the panel changes to clear cached QML components. Run the command inside your Omarchy session so its plugin commands can reach the shell. A disabled power widget is left disabled.

The integration follows the power panel shipped in [Omarchy 4.0.4](https://github.com/omacom/omarchy/releases/tag/v4.0.4). User-owned clones survive system updates; they do not automatically inherit upstream panel changes. Re-running `omabat integrate` updates Omabat's additions without replacing other customizations. An unfamiliar panel layout is left unchanged with an error; the collector can still run.

On older desktops without `omarchy-shell`, if Waybar has an expandable tray group, the integration adds a dynamic Nerd Font battery icon inside it. Clicking the icon opens Omabat. The existing Waybar configuration is backed up before modification.

No root privileges are required.

## Controls

| Key | Action |
| --- | --- |
| `g` / `h` | Open History / Health |
| `←` / `→` | Cycle history ranges |
| `1` / `2` / `3` | Select `24h` / `3d` / `7d` |
| `j` / `k`, `↑` / `↓` | Scroll |
| Page Up / Page Down | Scroll by page |
| Home / End | Jump to top / bottom |
| `r` | Refresh |
| `q` | Quit |

## Commands

```text
omabat [--range 24h|3d|7d]  Open the dashboard
omabat collect              Collect one sample
omabat collect --daemon     Run the collector daemon directly
omabat health               Print current health and available hardware details
omabat demo-data            Create a realistic demo history database
omabat install              Install and start the user service only
omabat integrate            Integrate with the desktop panel (may restart it)
omabat version              Print the installed version
```

## Try It With Demo Data

Create a realistic seven-day history without waiting for real samples:

```sh
omabat demo-data --db ./omabat-demo.db --days 7
omabat --db ./omabat-demo.db --range 7d
```

## Data And Privacy

All history stays on your machine in:

```text
${XDG_DATA_HOME:-~/.local/share}/omabat/omabat.db
```

Raw samples are retained for ten days. Hourly aggregates and discrete charging or suspend events are retained for longer-term history.

Omabat reads the primary system battery from UPower and sysfs. Hardware fields that the battery does not expose are omitted.

## Build From Source

Building requires Go 1.24.2 or newer:

```sh
git clone https://github.com/nuzair46/omabat.git
cd omabat
go build -o omabat ./cmd/omabat
./omabat
```

Run the test suite with:

```sh
go test ./...
```

## License

Omabat is available under the [MIT License](LICENSE).
