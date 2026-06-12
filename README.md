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
- Optional Waybar daemon-status indicator
- Local SQLite storage with no account or network service

## Install

Download the archive for your architecture from [GitHub Releases](https://github.com/nuzair46/omabat/releases/latest), then install the binary:

```sh
VERSION=0.1.0
ARCH=amd64
tar -xzf "omabat_${VERSION}_linux_${ARCH}.tar.gz"
install -Dm755 "omabat_${VERSION}_linux_${ARCH}/omabat" ~/.local/bin/omabat
```

Use `ARCH=arm64` on ARM systems. Ensure `~/.local/bin` is in your `PATH`, then launch Omabat:

```sh
omabat
```

Omabat requires Linux with `/sys/class/power_supply`. For the intended icon rendering, use a terminal configured with a [Nerd Font](https://www.nerdfonts.com/).

UPower, systemd-logind, `powerprofilesctl`, and Waybar are optional integrations. Omabat falls back to sysfs when UPower is unavailable.

## Start Collecting History

The dashboard records an immediate sample and refreshes every three seconds while open. To keep collecting after closing it, install the user daemon:

```sh
omabat install
```

This installs and starts `~/.config/systemd/user/omabat.service`. The daemon samples every 120 seconds and records readings immediately before suspend and after resume.

If Waybar is configured, `omabat install` also adds a Nerd Font daemon-status indicator. Clicking the indicator opens Omabat. The existing Waybar configuration is backed up before modification.

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
omabat install              Install and enable the user service
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
