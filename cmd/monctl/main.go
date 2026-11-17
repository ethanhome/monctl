// monctl - DDC/CI monitor control CLI.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bacnh85/monctl/internal/service"
	"github.com/bacnh85/monctl/monitor"
)

const usage = `monctl - control external monitors via DDC/CI

Usage:
  monctl scan                          List monitors with current state
  monctl get   <name> [-m N]           Read brightness|contrast|input|power
  monctl set   <name> <value> [-m N]   Set: number, +N/-N offset, input/power name, or 0xNN
  monctl vcp   <0xcode> [value] [-m N] Raw VCP read/write escape hatch
  monctl version                       Print the version

Examples:
  monctl set brightness 30             all monitors to 30
  monctl set brightness +10            offset, clamped
  monctl set input hdmi2               switch source
  monctl set power off                 sleep the panel
`

// version is injected at release time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "scan":
		return cmdScan()
	case "get", "set":
		return cmdGetSet(args[0], args[1:])
	case "vcp":
		return cmdVCP(args[1:])
	case "version", "-v", "--version":
		fmt.Printf("monctl %s\n", version)
		return 0
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "monctl: %v\n", err)
	return 1
}

func cmdScan() int {
	states, err := service.New().Scan()
	if err != nil {
		return fail(err)
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
		fmt.Printf("[%d] %s primary=%v %s\n", m.Index, m.Description, m.Primary, state)
	}
	return 0
}

func cmdGetSet(verb string, argv []string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	mIdx := fs.Int("m", 0, "monitor index (0 = all)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	ctl, err := service.LookupControl(rest[0])
	if err != nil {
		return fail(err)
	}
	var rawVal string
	if verb == "set" {
		if len(rest) < 2 {
			return fail(fmt.Errorf("set %s requires a value", ctl.Name))
		}
		rawVal = rest[1]
	}

	svc := service.New()
	targets, err := svc.Targets(*mIdx)
	if err != nil {
		return fail(err)
	}

	for _, m := range targets {
		if verb == "get" {
			r, err := svc.Get(m, ctl)
			if err != nil {
				return fail(err)
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
			fmt.Printf("[%d] %s = %d/%d%s\n", m.Index, ctl.Name, r.Cur, r.Max, extra)
			continue
		}
		if _, err := svc.Set(m, ctl, rawVal); err != nil {
			return fail(err)
		}
		fmt.Printf("[%d] %s = %s ok\n", m.Index, ctl.Name, rawVal)
	}
	return 0
}

func cmdVCP(argv []string) int {
	fs := flag.NewFlagSet("vcp", flag.ContinueOnError)
	mIdx := fs.Int("m", 0, "monitor index (0 = all)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	code, err := service.ParseHexOrDec(rest[0])
	if err != nil {
		return fail(fmt.Errorf("bad VCP code %q", rest[0]))
	}
	svc := service.New()
	targets, err := svc.Targets(*mIdx)
	if err != nil {
		return fail(err)
	}
	for _, m := range targets {
		if len(rest) < 2 {
			r, err := svc.Raw(m, code)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("[%d] 0x%02X = %d (max %d) 0x%02X\n", m.Index, code, r.Cur, r.Max, r.Cur)
			continue
		}
		val, err := service.ParseHexOrDec(rest[1])
		if err != nil {
			return fail(fmt.Errorf("bad value %q", rest[1]))
		}
		if err := svc.SetRaw(m, code, val); err != nil {
			return fail(err)
		}
		fmt.Printf("[%d] 0x%02X = 0x%02X ok\n", m.Index, code, val)
	}
	return 0
}
