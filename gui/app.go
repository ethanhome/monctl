// Package main is the Wails GUI shell for monctl. It exposes a small set of
// bindings to the frontend and keeps all DDC access on one serial lock.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/bacnh85/monctl/internal/service"
	"github.com/bacnh85/monctl/internal/settings"
	"github.com/bacnh85/monctl/internal/update"
	"github.com/bacnh85/monctl/monitor"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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

	update *updateController
}

// NewApp creates the app.
func NewApp() *App { return newApp(service.New()) }

func newApp(svc *service.Service) *App {
	return &App{
		svc: svc,
		seq: map[string]uint64{},
		update: newUpdateController(version, update.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
			&update.GitHubSource{}, &update.HTTPFetcher{}, nil),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.update == nil {
		return
	}
	// Keep the previous version around for a day, then clean it up. This is
	// best-effort: running from a disk image simply cannot have one.
	if target := installTarget(); target != "" {
		update.CleanupStaleOld(target, 24*time.Hour)
	}
	if st, err := a.settingsStore(); err == nil {
		a.update.setStore(st)
	} else {
		wruntime.LogError(ctx, "settings: "+err.Error())
	}
	a.update.setEmit(func(v UpdateView) { wruntime.EventsEmit(ctx, "update:state", v) })
	a.update.setRestart(a.relaunch)
	go func() {
		timer := time.NewTimer(autoCheckDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		_, _ = a.update.Check(ctx, false)
	}()
}

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

// MonitorView is one monitor as rendered by the UI.
type MonitorView struct {
	Index       int               `json:"index"`
	ID          string            `json:"id"` // settings key (EDID model)
	Description string            `json:"description"`
	Primary     bool              `json:"primary"`
	Input       InputView         `json:"input"`
	Brightness  LevelView         `json:"brightness"`
	Contrast    LevelView         `json:"contrast"`
	Labels      map[string]string `json:"labels"`
	// Recent lists input keys most recently switched to first. The UI keeps
	// the current source on top and orders the rest by this list.
	Recent []string `json:"recent"`
}

// ListMonitors scans monitors with their current input source and labels.
func (a *App) ListMonitors() ([]MonitorView, error) {
	a.ddcMu.Lock()
	infos, err := a.svc.List()
	if err != nil {
		a.ddcMu.Unlock()
		return nil, err
	}
	// Power (VCP 0xD6) is CLI-only: a panel put into standby/off often stops
	// answering DDC, so the GUI neither reads nor writes it.
	ictl, _ := service.LookupControl("input")
	bctl, _ := service.LookupControl("brightness")
	cctl, _ := service.LookupControl("contrast")
	input := make([]service.Reading, len(infos))
	bright := make([]service.Reading, len(infos))
	contrast := make([]service.Reading, len(infos))
	var ierrs, berrs, cerrs []error
	for i, m := range infos {
		input[i], err = a.svc.Get(m, ictl)
		ierrs = append(ierrs, err)
		bright[i], err = a.svc.Get(m, bctl)
		berrs = append(berrs, err)
		contrast[i], err = a.svc.Get(m, cctl)
		cerrs = append(cerrs, err)
	}
	a.ddcMu.Unlock()
	var prefs map[string]settings.Monitor
	if st, serr := a.settingsStore(); serr == nil {
		prefs = st.Snapshot().Monitors
	}
	out := make([]MonitorView, 0, len(infos))
	for i, m := range infos {
		id := settings.MonitorID(m.Description)
		pref := prefs[id]
		v := MonitorView{
			Index:       m.Index,
			ID:          id,
			Description: m.Description,
			Primary:     m.Primary,
			Labels:      pref.Labels,
			Recent:      pref.Recent,
		}
		if v.Labels == nil {
			v.Labels = map[string]string{}
		}
		if ierrs[i] == nil {
			v.Input = InputView{OK: true, Name: monitor.InputName(byte(input[i].Cur)), Raw: fmt.Sprintf("0x%02X", input[i].Cur)}
		}
		if berrs[i] == nil {
			v.Brightness = LevelView{OK: true, Percent: service.Percent(bright[i].Cur, bright[i].Max)}
		}
		if cerrs[i] == nil {
			v.Contrast = LevelView{OK: true, Percent: service.Percent(contrast[i].Cur, contrast[i].Max)}
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
	m, err := a.svc.ByIndex(idx)
	if err != nil {
		a.ddcMu.Unlock()
		return err
	}
	_, err = a.svc.Set(m, ctl, input)
	a.ddcMu.Unlock()
	if err != nil {
		return err
	}
	// Remember the target so the list can put recently used sources first.
	// Recording is best-effort: the switch itself already succeeded.
	if st, serr := a.settingsStore(); serr == nil {
		_ = st.RecordInputSwitch(settings.MonitorID(m.Description), inputKey(input))
	}
	return nil
}

// inputKey normalises a set-input value ("hdmi2", "0x12") to the key the UI
// uses for that row: the friendly name, or raw hex when it is unknown.
func inputKey(v string) string {
	b, err := monitor.ParseVCPValue(v)
	if err != nil {
		return v
	}
	if name := monitor.InputName(b); name != "" {
		return name
	}
	return fmt.Sprintf("0x%02X", b)
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

// AppVersion returns the running version shown in the footer.
func (a *App) AppVersion() string { return version }

// GetUpdateState returns the last update check result.
func (a *App) GetUpdateState() UpdateView {
	if a.update == nil {
		return UpdateView{}
	}
	return a.update.View()
}

// CheckForUpdates runs a manual check, bypassing the 24-hour throttle.
func (a *App) CheckForUpdates() (UpdateView, error) {
	if a.update == nil {
		return UpdateView{}, errors.New("update checker unavailable")
	}
	if a.ctx == nil {
		return UpdateView{}, errors.New("app is not ready")
	}
	return a.update.Check(a.ctx, true)
}

// StartUpdate downloads, verifies and replaces the running app bundle. The
// returned view tells the UI whether it ended in "done" or "failed"; the
// progress events arrive through "update:state".
func (a *App) StartUpdate() (UpdateView, error) {
	if a.update == nil {
		return UpdateView{}, errors.New("update checker unavailable")
	}
	if a.ctx == nil {
		return UpdateView{}, errors.New("app is not ready")
	}
	return a.update.StartUpgrade(a.ctx)
}

// RestartApp relaunches the updated app and quits the current process.
func (a *App) RestartApp() error {
	if a.update == nil {
		return errors.New("update checker unavailable")
	}
	return a.update.Restart()
}

// relaunch starts the installed version and quits this process. On macOS the
// bundle is opened through LaunchServices so the new instance is registered
// normally; on Windows the executable is started directly.
func (a *App) relaunch() error {
	if a.ctx == nil {
		return errors.New("app is not ready")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		bundle, err := update.LocateBundle(resolved)
		if err != nil {
			return err
		}
		if err := exec.Command("/usr/bin/open", "-n", bundle).Run(); err != nil {
			return fmt.Errorf("relaunch: %w", err)
		}
	case "windows":
		cmd := exec.Command(resolved)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("relaunch: %w", err)
		}
		_ = cmd.Process.Release()
	default:
		return errUnsupportedPlatform
	}
	wruntime.Quit(a.ctx)
	return nil
}

// installTarget is the path whose ".old" copy should be cleaned at startup:
// the bundle on macOS, the executable elsewhere. Empty when it cannot be
// determined (for example a dev run outside a bundle).
func installTarget() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	if runtime.GOOS == "darwin" {
		bundle, err := update.LocateBundle(resolved)
		if err != nil {
			return ""
		}
		return bundle
	}
	return resolved
}

// SetAutoCheckUpdates persists the "check on startup" preference.
func (a *App) SetAutoCheckUpdates(enabled bool) error {
	st, err := a.settingsStore()
	if err != nil {
		return err
	}
	if a.update != nil {
		a.update.setStore(st)
	}
	return st.SetDisableUpdateCheck(!enabled)
}

// OpenDownloadPage opens the release page for the available update.
func (a *App) OpenDownloadPage() {
	if a.ctx == nil {
		return
	}
	page := latestPage
	if a.update != nil {
		if v := a.update.View(); v.URL != "" {
			page = v.URL
		}
	}
	wruntime.BrowserOpenURL(a.ctx, page)
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
