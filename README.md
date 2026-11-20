# monctl

DDC/CI monitor control for Windows 10/11 and macOS (Apple Silicon): a CLI
and a small desktop app sharing one backend. Scan monitors, set
brightness/contrast/input/power — and, for a monitor shared by several
computers, switch which computer is on screen.

Verified live on:
- Windows: Samsung Odyssey G95NC via HDMI-2 (input `0x12`, brightness VCP max=50)
- macOS (M4): Samsung Odyssey G95NC via DisplayPort — brightness/contrast/input/power-set
  all working. Note: stock `m1ddc` cannot READ from this panel (it omits the
  sub-address from the get-request checksum, which the G95NC silently rejects);
  monctl sends spec-correct checksums and self-adapts the sub-address per retry.

## Install

Download from the [latest release](../../releases/latest):

| File | What it is |
|---|---|
| `monctl-gui-windows-amd64.exe` | Desktop app, Windows 10/11 (amd64) |
| `monctl-windows-amd64.exe` | CLI, Windows 10/11 (amd64) |
| `monctl-macos-arm64.dmg` | Desktop app, Apple Silicon |
| `monctl-darwin-arm64` | CLI, Apple Silicon |

The Windows app needs the WebView2 runtime. It ships with Windows 11 and
with most updated Windows 10 installs; if the window stays blank, install
the [WebView2 runtime](https://developer.microsoft.com/microsoft-edge/webview2/)
once.

The macOS app is not notarised. On first launch macOS refuses to open it:
right-click → Open, or run
`xattr -d com.apple.quarantine /Applications/monctl.app`. The CLI is a bare
binary; put it on the internal disk (e.g. `~/bin/monctl`).

`SHA256SUMS` and `monctl-macos-arm64.zip` are published with every release for
the updaters; you do not need to download them by hand.

## Upgrade

Both the CLI and the desktop app can update themselves from GitHub Releases.

### Desktop app

About five seconds after launch the app checks for a newer release, and at
most once every 24 hours. The check is silent: when offline or rate-limited
nothing is shown. When a newer version exists, a bar appears under the title
with a **更新 (Update)** button. Clicking it downloads that release, verifies
its SHA256 against the `SHA256SUMS` from the same release, and replaces the
running app in place. The old copy is kept beside it as `.old` for 24 hours.
Finish with **立即重启 (Restart now)**, or **稍后 (Later)** to restart yourself.

The footer shows the installed version and has two buttons: **检查更新 (Check
for updates)**, which bypasses the daily limit, and **设置 (Settings)**, where
the startup check can be turned off or on.

Updates never request sudo/UAC and never touch a location they cannot write.
When the running copy is in such a place, clicking the update button stops
before downloading and the bar shows the reason plus a download-page button:

- macOS, launched from the mounted `.dmg` (or from an App Translocation path):
  drag `monctl.app` into `/Applications` first.
- macOS, `/Applications` not writable (installed by another admin): install
  the `.dmg` manually.
- Windows, the folder holding the exe not writable (for example
  `C:\Program Files`).

The first macOS install is unsigned and not notarised, so the right-click →
Open step described under Install is needed once. Files written by the updater
do not carry `com.apple.quarantine`, so self-updates do not repeat that
prompt.

### Command line

    monctl upgrade            # check, print the plan, ask y/N
    monctl upgrade --check    # check only
    monctl upgrade --yes      # install without asking

Exit codes:

| Code | Meaning |
|---|---|
| `0` | Up to date, upgraded, cancelled, or plan printed because stdin is not a terminal |
| `1` | Upgrade failed, or the check was refused (a `dev` build) |
| `2` | Usage error |
| `10` | `--check` found a newer release |
| `11` | `--check` could not reach the update source |

Without `--yes`, when stdin is not a terminal the plan is printed but nothing
is installed. `dev` builds refuse to check.

### Manual recovery

Every update leaves the previous version next to the current one for 24 hours,
in case the new one does not start. Quit the app first.

macOS — run this in the folder that holds `monctl.app`, normally
`/Applications`:

    mv monctl.app monctl.app.broken
    mv monctl.app.old monctl.app

Windows GUI — run this in the folder holding the exe (the release name is
shown; substitute your own if you renamed it):

    cd <folder containing the exe>
    ren monctl-gui-windows-amd64.exe monctl-gui-windows-amd64.exe.broken
    ren monctl-gui-windows-amd64.exe.old monctl-gui-windows-amd64.exe

Windows CLI from the release files — in the folder holding the CLI exe:

    ren monctl-windows-amd64.exe monctl-windows-amd64.exe.broken
    ren monctl-windows-amd64.exe.old monctl-windows-amd64.exe

CLI installs use the same `.old` scheme beside the `monctl` / `monctl.exe`
binary. The `.old` copy is deleted at the next launch once it is more than 24
hours old.

## GUI

The window lists every detected monitor; its title bar shows the app version.
For each monitor:

- **Input sources** are listed under a heading, current source first. That
  source is marked as current and is not clickable — this computer *is*
  that source, so switching to it is meaningless. Every other source has a
  switch button; sources you switched to recently come first.
- Clicking a switch button asks for confirmation; tick "don't ask again" to
  switch without the prompt from then on.
- **After switching, the window deliberately does not change.** The current
  marker stays where it was, because that is still the truth. Switching
  back later shows exactly the screen you left.
- Click a source's name to label it (e.g. "Work PC"). Labels are stored per
  monitor model in the user config directory.
- Brightness and contrast appear below the list, but only when the current
  input source can be read; a control the panel cannot report is hidden
  individually.

The source list is the generic MCCS set (VGA, DVI, DP1, DP2, HDMI1, HDMI2,
USB-C). Panels report their capabilities inconsistently, so ports a monitor
does not have are still listed; the window says so.

### Switching between computers

Install monctl on every computer attached to the monitor. To move the
screen, use the computer you are currently on and switch to the other
one's input. To come back, use that other computer (or the monitor's own
buttons) — once the panel shows another input, this computer cannot
control it any more. There is no automatic switch-back.

## CLI

    monctl scan                        # list monitors (EDID model) + input/power
    monctl get  brightness|contrast|input|power [-m N]
    monctl set  <name> <value> [-m N]  # value: percent, +N/-N offset, name, or 0xNN
    monctl vcp  <0xcode> [value]       # raw DDC escape hatch
    monctl upgrade [--check] [--yes]   # update to the latest release
    monctl version

Brightness/contrast values are **percent 0-100**, scaled to the panel-reported
VCP max (G95NC reports 50). Inputs: vga dvi dp1 dp2 hdmi1 hdmi2 usb-c.
Power: on off standby. Requires monitor OSD setting **DDC/CI: Enabled**.
A panel put into standby/off often stops answering DDC, so the app cannot
wake it again; use the monitor's own power button or restore the input
signal. This is why power is CLI-only and no longer offered in the GUI.

Examples:

    monctl set brightness 30
    monctl set brightness +10
    monctl set input hdmi2
    monctl set power off

### macOS notes (Apple Silicon)

- Uses the private `IOAVService` DDC/CI API (same as m1ddc / MonitorControl).
  Intel Macs are not supported.
- macOS 27: DDC needs the panel on the Mac's input (KVM). When the monitor is
  switched away, macOS synthesizes a headless virtual display
  (`kCGDisplayIsVirtualDevice`); monctl skips it in `scan` instead of showing
  a phantom display with a misleading OSD hint.
- Panels disagree on the DDC sub-address; monctl alternates 0x51 (standard)
  and 0x50 (some Samsungs) per retry. Force one with `MONCTL_DDC_ADDR=50`;
  tune the reply wait with `MONCTL_DDC_DELAY_MS` (default 40ms per spec).
- `MONCTL_DEBUG=1` dumps raw DDC reply frames to stderr.
- Quit DDC-polling apps (BetterDisplay, Lunar, MonitorControl) when debugging
  DDC — concurrent bus access corrupts reads.
- G95NC quirks: `get power` returns 0/0 (the panel accepts power-mode writes
  but does not report the state back); `set power off/standby` works.
- G95NC power-writes are intermittent: the panel goes through minutes-long
  phases of ACKing 0xD6 writes without acting (brightness/input unaffected).
  Re-issue `set power` if the first attempt is swallowed.

## Build

    go build -o bin/monctl.exe ./cmd/monctl        # CLI, Windows
    go build -o bin/monctl ./cmd/monctl            # CLI, macOS (CGO + Xcode CLT)

    cd gui && wails build                          # GUI -> gui/build/bin/

The GUI requires the [Wails CLI](https://wails.io) v2.17.1, Node, and (on
macOS) Xcode CLT. Only Windows amd64 and macOS arm64 are release targets.

## Layout

    cmd/monctl/        CLI dispatch (scan / get / set / vcp / upgrade / version)
    internal/service/  Shared business layer: control lookup, value parsing,
                       VCP max scaling, per-monitor get/set (+ tests)
    internal/settings/ GUI preferences: input labels, skip-switch-confirm,
                       update-check preference and last check time
    internal/update/   Release check, SHA256 verify, safe zip unpack, in-place
                       binary/bundle installers (+ tests)
    gui/               Wails app (separate module), vanilla JS frontend;
                       update.go = GUI update state machine and events
    monitor/           Controller seam; windows.go = dxva2/user32 syscalls;
                       darwin.go = IOAVService DDC (Apple Silicon, cgo);
                       vcp.go = VCP codes + input map (+ tests)
