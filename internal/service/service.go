// Package service is the business layer shared by the CLI and the GUI:
// control-name lookup, value parsing (percent, +N/-N offsets, names, 0xNN)
// and VCP max scaling. It never prints, exits, or parses flags.
package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bacnh85/monctl/monitor"
)

// Control is a named VCP control accepted by Get/Set.
type Control struct {
	Name string
	Code byte
}

// LookupControl resolves brightness|contrast|input|power (case-insensitive).
func LookupControl(name string) (Control, error) {
	name = strings.ToLower(name)
	code, ok := monitor.KnownVCPCodes()[name]
	if !ok {
		return Control{}, fmt.Errorf("unknown control %q (brightness|contrast|input|power)", name)
	}
	return Control{Name: name, Code: code}, nil
}

// Continuous reports whether the control takes a 0-100 percent value
// scaled to the panel-reported VCP max.
func (c Control) Continuous() bool { return c.Name == "brightness" || c.Name == "contrast" }

// Reading is a VCP read result. Err is set when the panel did not answer.
type Reading struct {
	Cur, Max int
	Err      error
}

// MonitorState is one monitor with its input/power readings (scan view).
type MonitorState struct {
	monitor.Info
	Input Reading
	Power Reading
}

// Service wraps a monitor.Controller.
type Service struct{ ctrl monitor.Controller }

// New returns a Service backed by the platform controller.
func New() *Service { return &Service{ctrl: monitor.New()} }

// NewWithController returns a Service backed by ctrl (tests).
func NewWithController(ctrl monitor.Controller) *Service { return &Service{ctrl: ctrl} }

// Scan lists monitors with their current input and power readings.
func (s *Service) Scan() ([]MonitorState, error) {
	infos, err := s.ctrl.List()
	if err != nil {
		return nil, err
	}
	out := make([]MonitorState, 0, len(infos))
	for _, m := range infos {
		st := MonitorState{Info: m}
		st.Input.Cur, st.Input.Max, st.Input.Err = s.ctrl.GetVCP(m, monitor.VCPInput)
		st.Power.Cur, st.Power.Max, st.Power.Err = s.ctrl.GetVCP(m, monitor.VCPPower)
		out = append(out, st)
	}
	return out, nil
}

// Targets resolves a monitor index (0 = all).
func (s *Service) Targets(mIdx int) ([]monitor.Info, error) {
	sel := "@all"
	if mIdx > 0 {
		sel = strconv.Itoa(mIdx)
	}
	return monitor.ResolveTargets(s.ctrl, sel)
}

// ByIndex returns the monitor with the given index, 0 included. Unlike
// Targets (the CLI's "-m" selector), index 0 does not mean "all".
func (s *Service) ByIndex(idx int) (monitor.Info, error) {
	all, err := s.ctrl.List()
	if err != nil {
		return monitor.Info{}, err
	}
	for _, m := range all {
		if m.Index == idx {
			return m, nil
		}
	}
	return monitor.Info{}, fmt.Errorf("no monitor with index %d", idx)
}

// Get reads a control's current value and panel max.
func (s *Service) Get(m monitor.Info, c Control) (Reading, error) {
	return s.Raw(m, c.Code)
}

// Raw reads an arbitrary VCP code.
func (s *Service) Raw(m monitor.Info, code byte) (Reading, error) {
	cur, maxV, err := s.ctrl.GetVCP(m, code)
	if err != nil {
		return Reading{}, err
	}
	return Reading{Cur: cur, Max: maxV}, nil
}

// SetRaw writes an arbitrary VCP code.
func (s *Service) SetRaw(m monitor.Info, code, val byte) error {
	return s.ctrl.SetVCP(m, code, val)
}

// Set handles "+N"/"-N" offsets and absolute values.
// Continuous controls (brightness/contrast): inputs are PERCENT 0-100,
// scaled to the panel-reported max (e.g. G95NC reports max=50).
// It returns the resulting percent for offset/continuous writes, else 0.
func (s *Service) Set(m monitor.Info, c Control, rawVal string) (int, error) {
	if strings.HasPrefix(rawVal, "+") || strings.HasPrefix(rawVal, "-") {
		cur, maxV, err := s.ctrl.GetVCP(m, c.Code)
		if err != nil {
			return 0, err
		}
		delta, err := strconv.Atoi(rawVal)
		if err != nil {
			return 0, fmt.Errorf("bad offset %q", rawVal)
		}
		hi := maxV
		if hi <= 0 {
			hi = 100
		}
		// Offset is expressed in percent points.
		raw := cur + delta*hi/100
		v := byte(clamp(raw, 0, hi))
		return percent(int(v), hi), s.ctrl.SetVCP(m, c.Code, v)
	}
	b, err := monitor.ParseVCPValue(rawVal)
	if err != nil {
		return 0, err
	}
	if c.Continuous() {
		_, maxV, err := s.ctrl.GetVCP(m, c.Code)
		if err != nil {
			return 0, err
		}
		if b > 100 {
			return 0, fmt.Errorf("value %d out of range 0-100", b)
		}
		hi := maxV
		if hi <= 0 {
			hi = 100
		}
		b = byte(int(b) * hi / 100)
		return percent(int(b), hi), s.ctrl.SetVCP(m, c.Code, b)
	}
	return 0, s.ctrl.SetVCP(m, c.Code, b)
}

// ParseHexOrDec parses a raw VCP code/value: hex first (with or without
// 0x), falling back to decimal when it is not valid hex.
func ParseHexOrDec(s string) (byte, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 8)
	if err != nil {
		n2, err2 := strconv.ParseUint(s, 10, 8)
		if err2 != nil {
			return 0, err
		}
		n = n2
	}
	return byte(n), nil
}

// PowerName is the friendly name of a VCP 0xD6 value, or 0xNN if unknown.
func PowerName(v byte) string {
	switch v {
	case monitor.PowerOn:
		return "on"
	case monitor.PowerOff:
		return "off"
	case monitor.PowerStandby:
		return "standby"
	}
	return fmt.Sprintf("0x%02X", v)
}

func percent(v, hi int) int {
	if hi == 0 {
		return 0
	}
	return v * 100 / hi
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
