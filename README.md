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

## GUI

The window lists every detected monitor. For each monitor:

- **Input sources** are listed under a heading, current source first. That
  source is marked as current and is not clickable — this computer *is*
  that source, so switching to it is meaningless. Every other source has a
  switch button.
- Clicking a switch button asks for confirmation; tick "don't ask again" to
  switch without the prompt from then on.
- **After switching, the window deliberately does not change.** The current
  marker stays where it was, because that is still the truth. Switching
  back later shows exactly the screen you left.
- Click a source's name to label it (e.g. "Work PC"). Labels are stored per
  monitor model in the user config directory.
- Brightness and contrast appear below the list, but only when the current
  input source can be read; a control the panel cannot report is hidden
  individually. Power offers on, standby and off, and turning the monitor
  off asks for confirmation first.

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
    monctl version

Brightness/contrast values are **percent 0-100**, scaled to the panel-reported
VCP max (G95NC reports 50). Inputs: vga dvi dp1 dp2 hdmi1 hdmi2 usb-c.
Power: on off standby. Requires monitor OSD setting **DDC/CI: Enabled**.

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

    cmd/monctl/        CLI dispatch (scan / get / set / vcp / version)
    internal/service/  Shared business layer: control lookup, value parsing,
                       VCP max scaling, per-monitor get/set (+ tests)
    internal/settings/ GUI preferences: input labels, skip-switch-confirm
    gui/               Wails app (separate module), vanilla JS frontend
    monitor/           Controller seam; windows.go = dxva2/user32 syscalls;
                       darwin.go = IOAVService DDC (Apple Silicon, cgo);
                       vcp.go = VCP codes + input map (+ tests)
