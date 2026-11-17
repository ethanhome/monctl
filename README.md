# monctl

DDC/CI monitor control CLI for Windows and macOS (Apple Silicon). Single Go
binary — scan and control brightness/contrast/input/power.

Verified live on:
- Windows: Samsung Odyssey G95NC via HDMI-2 (input `0x12`, brightness VCP max=50)
- macOS (M4): Samsung Odyssey G95NC via DisplayPort — brightness/contrast/input/power-set
  all working. Note: stock `m1ddc` cannot READ from this panel (it omits the
  sub-address from the get-request checksum, which the G95NC silently rejects);
  monctl sends spec-correct checksums and self-adapts the sub-address per retry.

## Build

    go build -o bin/monctl.exe .                                  # Windows
    go build -o bin/monctl .                                      # macOS (needs CGO + Xcode CLT)

## Usage

    monctl scan                        # list monitors (EDID model) + input/power
    monctl get  brightness|contrast|input|power [-m N]
    monctl set  <name> <value> [-m N]  # value: percent, +N/-N offset, name, or 0xNN
    monctl vcp  <0xcode> [value]       # raw DDC escape hatch

Brightness/contrast values are **percent 0-100**, scaled to the panel-reported
VCP max (G95NC reports 50). Inputs: vga dvi dp1 dp2 hdmi1 hdmi2 usb-c.
Power: on off standby. Requires monitor OSD setting **DDC/CI: Enabled**.

### macOS notes (Apple Silicon)

- Uses the private `IOAVService` DDC/CI API (same as m1ddc / MonitorControl).
  Intel Macs are not supported.
- Install the binary on the **internal disk** (e.g. `~/bin/monctl`), not an
  external volume — launchd-spawned processes can stall in dyld opening
  binaries on external drives.
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

Examples:

    monctl set brightness 30
    monctl set brightness +10
    monctl set input hdmi2
    monctl set power off

## Layout

    main.go            CLI dispatch (scan / get / set / vcp)
    monitor/           Controller seam; windows.go = dxva2/user32 syscalls;
                       darwin.go = IOAVService DDC (Apple Silicon, cgo);
                       vcp.go = VCP codes + input map (+ tests)
