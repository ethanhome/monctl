// monctl - DDC/CI monitor control CLI.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bacnh85/monctl/internal/service"
	"github.com/bacnh85/monctl/internal/update"
	"github.com/bacnh85/monctl/monitor"
	"golang.org/x/term"
)

const usage = `monctl - control external monitors via DDC/CI

Usage:
  monctl scan                          List monitors with current state
  monctl get   <name> [-m N]           Read brightness|contrast|input|power
  monctl set   <name> <value> [-m N]   Set: number, +N/-N offset, input/power name, or 0xNN
  monctl vcp   <0xcode> [value] [-m N] Raw VCP read/write escape hatch
  monctl upgrade [--check] [--yes]     Upgrade to the latest release
  monctl version                       Print the version

Examples:
  monctl set brightness 30             all monitors to 30
  monctl set brightness +10            offset, clamped
  monctl set input hdmi2               switch source
  monctl set power off                 sleep the panel
`

// Exit codes, aligned with the upgrade design.
const (
	exitOK       = 0
	exitFail     = 1
	exitUsage    = 2
	exitNewer    = 10 // upgrade --check found a newer release
	exitCheckErr = 11 // upgrade --check could not check
)

// version is injected at release time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// deps holds the process-level dependencies so commands can be driven by
// tests without touching the network or the real standard streams.
type deps struct {
	version     string
	source      update.Source
	fetcher     update.Fetcher
	installer   update.Installer
	target      string
	platform    update.Platform
	in          io.Reader
	interactive bool
	out         io.Writer
	errOut      io.Writer
}

func main() {
	d := realDeps()
	if d.target != "" {
		// Best-effort: keep the rollback copy for a day after an update.
		update.CleanupStaleOld(d.target, 24*time.Hour)
	}
	os.Exit(run(os.Args[1:], d))
}

func realDeps() deps {
	d := deps{
		version:  version,
		source:   &update.GitHubSource{},
		fetcher:  &update.HTTPFetcher{},
		platform: update.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		in:       os.Stdin,
		out:      os.Stdout,
		errOut:   os.Stderr,
	}
	d.interactive = isTerminal(os.Stdin)
	if exe, err := os.Executable(); err == nil {
		// Replace the real file, never the symlink that may point at it.
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			d.target = resolved
			d.installer = &update.BinaryInstaller{Target: resolved}
		}
	}
	return d
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func run(args []string, d deps) int {
	if len(args) == 0 {
		fmt.Fprint(d.errOut, usage)
		return exitUsage
	}
	switch args[0] {
	case "scan":
		return cmdScan(d)
	case "get", "set":
		return cmdGetSet(d, args[0], args[1:])
	case "vcp":
		return cmdVCP(d, args[1:])
	case "upgrade":
		return cmdUpgrade(d, args[1:])
	case "version", "-v", "--version":
		fmt.Fprintf(d.out, "monctl %s\n", d.version)
		return exitOK
	case "-h", "--help", "help":
		fmt.Fprint(d.out, usage)
		return exitOK
	default:
		fmt.Fprintf(d.errOut, "unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

func fail(d deps, err error) int {
	fmt.Fprintf(d.errOut, "monctl: %v\n", err)
	return exitFail
}

func cmdScan(d deps) int {
	states, err := service.New().Scan()
	if err != nil {
		return fail(d, err)
	}
	for _, m := range states {
		state := "input=? power=?"
		switch {
		case m.Input.Err == nil && m.Power.Err == nil:
			in := monitor.InputName(byte(m.Input.Cur))
			if in == "" {
				in = fmt.Sprintf("0x%02X", m.Input.Cur)
			}
			state = fmt.Sprintf("input=%s power=%d", in, m.Power.Cur)
		case m.Input.Err != nil:
			state = fmt.Sprintf("DDC unavailable: %v", m.Input.Err)
		}
		fmt.Fprintf(d.out, "[%d] %s primary=%v %s\n", m.Index, m.Description, m.Primary, state)
	}
	return exitOK
}

func cmdGetSet(d deps, verb string, argv []string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(d.errOut)
	mIdx := fs.Int("m", 0, "monitor index (0 = all)")
	if err := fs.Parse(argv); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprint(d.errOut, usage)
		return exitUsage
	}
	ctl, err := service.LookupControl(rest[0])
	if err != nil {
		return fail(d, err)
	}
	var rawVal string
	if verb == "set" {
		if len(rest) < 2 {
			return fail(d, fmt.Errorf("set %s requires a value", ctl.Name))
		}
		rawVal = rest[1]
	}

	svc := service.New()
	targets, err := svc.Targets(*mIdx)
	if err != nil {
		return fail(d, err)
	}

	for _, m := range targets {
		if verb == "get" {
			r, err := svc.Get(m, ctl)
			if err != nil {
				return fail(d, err)
			}
			extra := ""
			switch ctl.Code {
			case monitor.VCPInput:
				if n := monitor.InputName(byte(r.Cur)); n != "" {
					extra = fmt.Sprintf(" (%s)", n)
				}
			case monitor.VCPPower:
				extra = fmt.Sprintf(" (%s)", service.PowerName(byte(r.Cur)))
			}
			fmt.Fprintf(d.out, "[%d] %s = %d/%d%s\n", m.Index, ctl.Name, r.Cur, r.Max, extra)
			continue
		}
		if _, err := svc.Set(m, ctl, rawVal); err != nil {
			return fail(d, err)
		}
		fmt.Fprintf(d.out, "[%d] %s = %s ok\n", m.Index, ctl.Name, rawVal)
	}
	return exitOK
}

func cmdVCP(d deps, argv []string) int {
	fs := flag.NewFlagSet("vcp", flag.ContinueOnError)
	fs.SetOutput(d.errOut)
	mIdx := fs.Int("m", 0, "monitor index (0 = all)")
	if err := fs.Parse(argv); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprint(d.errOut, usage)
		return exitUsage
	}
	code, err := service.ParseHexOrDec(rest[0])
	if err != nil {
		return fail(d, fmt.Errorf("bad VCP code %q", rest[0]))
	}
	svc := service.New()
	targets, err := svc.Targets(*mIdx)
	if err != nil {
		return fail(d, err)
	}
	for _, m := range targets {
		if len(rest) < 2 {
			r, err := svc.Raw(m, code)
			if err != nil {
				return fail(d, err)
			}
			fmt.Fprintf(d.out, "[%d] 0x%02X = %d (max %d) 0x%02X\n", m.Index, code, r.Cur, r.Max, r.Cur)
			continue
		}
		val, err := service.ParseHexOrDec(rest[1])
		if err != nil {
			return fail(d, fmt.Errorf("bad value %q", rest[1]))
		}
		if err := svc.SetRaw(m, code, val); err != nil {
			return fail(d, err)
		}
		fmt.Fprintf(d.out, "[%d] 0x%02X = 0x%02X ok\n", m.Index, code, val)
	}
	return exitOK
}

// cmdUpgrade checks for a newer release and, after confirmation, replaces
// the running binary in place.
func cmdUpgrade(d deps, argv []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(d.errOut)
	check := fs.Bool("check", false, "only check for a newer release")
	yes := fs.Bool("yes", false, "install without asking for confirmation")
	if err := fs.Parse(argv); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(d.errOut, "monctl: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	up := &update.Upgrader{
		Source:    d.source,
		Fetcher:   d.fetcher,
		Installer: d.installer,
		Current:   d.version,
		Platform:  d.platform,
	}
	res, err := up.Check(context.Background())
	switch {
	case errors.Is(err, update.ErrNotReleaseBuild):
		return fail(d, err)
	case err != nil:
		if *check {
			fmt.Fprintf(d.errOut, "monctl: check for updates: %v\n", err)
			return exitCheckErr
		}
		return fail(d, fmt.Errorf("check for updates: %w", err))
	}
	if !res.Available {
		fmt.Fprintf(d.out, "monctl %s is up to date\n", res.Current)
		return exitOK
	}
	fmt.Fprintf(d.out, "update available: %s -> %s\n  %s\n", res.Current, res.Latest, res.Asset.Name)
	if *check {
		return exitNewer
	}

	if d.target != "" {
		fmt.Fprintf(d.out, "  replace %s\n", d.target)
	}
	if d.installer == nil {
		return fail(d, errors.New("cannot locate the running executable"))
	}
	if !*yes {
		if !d.interactive {
			fmt.Fprintln(d.out, "not installing: stdin is not a terminal; re-run with --yes")
			return exitOK
		}
		if !confirmUpgrade(d) {
			fmt.Fprintln(d.out, "cancelled")
			return exitOK
		}
	}
	if err := up.Apply(context.Background(), res); err != nil {
		return fail(d, fmt.Errorf("upgrade: %w", err))
	}
	fmt.Fprintf(d.out, "updated to %s\n", res.Latest)
	return exitOK
}

func confirmUpgrade(d deps) bool {
	fmt.Fprint(d.out, "Install? [y/N] ")
	line, _ := bufio.NewReader(d.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
