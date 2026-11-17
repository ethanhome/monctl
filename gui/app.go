// Package main is the Wails GUI shell for monctl. It exposes a small set of
// bindings to the frontend and keeps all DDC access on one serial lock.
package main

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/bacnh85/monctl/internal/service"
	"github.com/bacnh85/monctl/internal/settings"
	"github.com/bacnh85/monctl/monitor"
)

// App is the Wails-bound backend.
type App struct {
	ctx context.Context
	svc *service.Service

	// ddcMu serialises every DDC transaction: the bus is slow and concurrent
	// access corrupts reads (and the platform backends are not re-entrant).
	ddcMu sync.Mutex

	// seq counts slider requests per control so queued writes that a newer
	// value has superseded can be dropped instead of hitting the bus.
	seqMu sync.Mutex
	seq   map[string]uint64

	mu sync.Mutex
	st *settings.Store
}

// NewApp creates the app.
func NewApp() *App { return newApp(service.New()) }

func newApp(svc *service.Service) *App {
	return &App{svc: svc, seq: map[string]uint64{}}
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// InputView is a monitor's current input source.
type InputView struct {
	OK bool `json:"ok"`
	// Name is the friendly name ("hdmi2"), or "" when the panel reported a
	// value that is not in the table.
	Name string `json:"name"`
	// Raw is the raw VCP value, e.g. "0x1B".
	Raw string `json:"raw"`
}

// LevelView is a continuous control (brightness/contrast) as a percentage.
// OK is false when the panel did not answer or does not support the control;
// the UI then hides just that row.
type LevelView struct {
	OK      bool `json:"ok"`
	Percent int  `json:"percent"`
}

// PowerView is the current power state. OK is false when the panel does not
// report a recognised state (some panels return 0/0 and never do).
type PowerView struct {
	OK    bool   `json:"ok"`
	State string `json:"state"` // on|off|standby
}

// MonitorView is one monitor as rendered by the UI.
type MonitorView struct {
	Index       int               `json:"index"`
	ID          string            `json:"id"` // settings key (EDID model)
	Description string            `json:"description"`
	Primary     bool              `json:"primary"`
	Input       InputView         `json:"input"`
	Brightness  LevelView         `json:"brightness"`
	Contrast    LevelView         `json:"contrast"`
	Power       PowerView         `json:"power"`
	Labels      map[string]string `json:"labels"`
}

// ListMonitors scans monitors with their current input source and labels.
func (a *App) ListMonitors() ([]MonitorView, error) {
	a.ddcMu.Lock()
	states, err := a.svc.Scan()
	if err != nil {
		a.ddcMu.Unlock()
		return nil, err
	}
	// Scan covers input and power; brightness/contrast are only needed by the
	// GUI, so they are read here rather than in the shared Scan.
	bctl, _ := service.LookupControl("brightness")
	cctl, _ := service.LookupControl("contrast")
	bright := make([]service.Reading, len(states))
	contrast := make([]service.Reading, len(states))
	var berrs, cerrs []error
	for i, s := range states {
		bright[i], err = a.svc.Get(s.Info, bctl)
		berrs = append(berrs, err)
		contrast[i], err = a.svc.Get(s.Info, cctl)
		cerrs = append(cerrs, err)
	}
	a.ddcMu.Unlock()
	var labels map[string]map[string]string
	if st, serr := a.settingsStore(); serr == nil {
		labels = map[string]map[string]string{}
		for id, m := range st.Snapshot().Monitors {
			labels[id] = m.Labels
		}
	}
	out := make([]MonitorView, 0, len(states))
	for i, s := range states {
		id := settings.MonitorID(s.Description)
		v := MonitorView{
			Index:       s.Index,
			ID:          id,
			Description: s.Description,
			Primary:     s.Primary,
			Labels:      labels[id],
		}
		if v.Labels == nil {
			v.Labels = map[string]string{}
		}
		if s.Input.Err == nil {
			v.Input = InputView{OK: true, Name: monitor.InputName(byte(s.Input.Cur)), Raw: fmt.Sprintf("0x%02X", s.Input.Cur)}
		}
		if berrs[i] == nil {
			v.Brightness = LevelView{OK: true, Percent: service.Percent(bright[i].Cur, bright[i].Max)}
		}
		if cerrs[i] == nil {
			v.Contrast = LevelView{OK: true, Percent: service.Percent(contrast[i].Cur, contrast[i].Max)}
		}
		if s.Power.Err == nil {
			switch name := service.PowerName(byte(s.Power.Cur)); name {
			case "on", "off", "standby":
				v.Power = PowerView{OK: true, State: name}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// SetInput switches monitor idx to input (dp1, hdmi1, 0x1B, ...). The UI
// sends this after the user confirms; nothing is retried.
func (a *App) SetInput(idx int, input string) error {
	ctl, err := service.LookupControl("input")
	if err != nil {
		return err
	}
	a.ddcMu.Lock()
	defer a.ddcMu.Unlock()
	m, err := a.svc.ByIndex(idx)
	if err != nil {
		return err
	}
	_, err = a.svc.Set(m, ctl, input)
	return err
}

// applyLatest runs fn under the DDC lock unless a newer request with the
// same key arrived while this one was waiting: then the older value is stale
// and is dropped, so a fast slider drag only writes its final position.
func (a *App) applyLatest(key string, fn func() error) error {
	a.seqMu.Lock()
	a.seq[key]++
	gen := a.seq[key]
	a.seqMu.Unlock()
	return a.runIfLatest(key, gen, fn)
}

func (a *App) runIfLatest(key string, gen uint64, fn func() error) error {
	a.ddcMu.Lock()
	defer a.ddcMu.Unlock()
	a.seqMu.Lock()
	stale := a.seq[key] != gen
	a.seqMu.Unlock()
	if stale {
		return nil
	}
	return fn()
}

// SetBrightness sets brightness as a percentage 0-100.
func (a *App) SetBrightness(idx, pct int) error { return a.setLevel(idx, "brightness", pct) }

// SetContrast sets contrast as a percentage 0-100.
func (a *App) SetContrast(idx, pct int) error { return a.setLevel(idx, "contrast", pct) }

func (a *App) setLevel(idx int, name string, pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("%s %d out of range 0-100", name, pct)
	}
	ctl, err := service.LookupControl(name)
	if err != nil {
		return err
	}
	return a.applyLatest(fmt.Sprintf("%d:%s", idx, name), func() error {
		m, err := a.svc.ByIndex(idx)
		if err != nil {
			return err
		}
		_, err = a.svc.Set(m, ctl, strconv.Itoa(pct))
		return err
	})
}

// SetPower sets the power mode: on, off or standby.
func (a *App) SetPower(idx int, state string) error {
	switch state {
	case "on", "off", "standby":
	default:
		return fmt.Errorf("invalid power state %q", state)
	}
	ctl, err := service.LookupControl("power")
	if err != nil {
		return err
	}
	a.ddcMu.Lock()
	defer a.ddcMu.Unlock()
	m, err := a.svc.ByIndex(idx)
	if err != nil {
		return err
	}
	_, err = a.svc.Set(m, ctl, state)
	return err
}

// SetSkipSwitchConfirm remembers whether the switch confirmation is skipped.
func (a *App) SetSkipSwitchConfirm(skip bool) error {
	st, err := a.settingsStore()
	if err != nil {
		return err
	}
	return st.SetSkipSwitchConfirm(skip)
}

// GetSettings returns the persisted preferences.
func (a *App) GetSettings() (settings.Settings, error) {
	st, err := a.settingsStore()
	if err != nil {
		return settings.Settings{}, err
	}
	return st.Snapshot(), nil
}

// SaveLabel stores a display name for an input source; an empty name clears it.
func (a *App) SaveLabel(monitorID, input, name string) error {
	st, err := a.settingsStore()
	if err != nil {
		return err
	}
	return st.SetLabel(monitorID, input, name)
}

func (a *App) settingsStore() (*settings.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st != nil {
		return a.st, nil
	}
	path, err := settings.DefaultPath()
	if err != nil {
		return nil, err
	}
	st, err := settings.Open(path)
	if err != nil {
		return nil, err
	}
	a.st = st
	return st, nil
}
