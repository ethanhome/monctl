// Package main is the Wails GUI shell for monctl. It exposes a small set of
// bindings to the frontend and keeps all DDC access on one serial lock.
package main

import (
	"context"
	"fmt"
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

	mu sync.Mutex
	st *settings.Store
}

// NewApp creates the app.
func NewApp() *App { return &App{svc: service.New()} }

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

// MonitorView is one monitor as rendered by the UI.
type MonitorView struct {
	Index       int               `json:"index"`
	ID          string            `json:"id"` // settings key (EDID model)
	Description string            `json:"description"`
	Primary     bool              `json:"primary"`
	Input       InputView         `json:"input"`
	Labels      map[string]string `json:"labels"`
}

// ListMonitors scans monitors with their current input source and labels.
func (a *App) ListMonitors() ([]MonitorView, error) {
	a.ddcMu.Lock()
	states, err := a.svc.Scan()
	a.ddcMu.Unlock()
	if err != nil {
		return nil, err
	}
	var labels map[string]map[string]string
	if st, serr := a.settingsStore(); serr == nil {
		labels = map[string]map[string]string{}
		for id, m := range st.Snapshot().Monitors {
			labels[id] = m.Labels
		}
	}
	out := make([]MonitorView, 0, len(states))
	for _, s := range states {
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
