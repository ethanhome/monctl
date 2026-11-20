package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/bacnh85/monctl/internal/settings"
	"github.com/bacnh85/monctl/internal/update"
)

const (
	autoCheckDelay = 5 * time.Second
	checkInterval  = 24 * time.Hour
	releasePage    = "https://github.com/ethanhome/monctl/releases/tag/"
	latestPage     = "https://github.com/ethanhome/monctl/releases/latest"
)

var (
	errCheckInFlight       = errors.New("update already in progress")
	errUnsupportedPlatform = errors.New("in-app upgrade is not supported on this platform")
)

// UpdateView is the update state rendered by the frontend. State is "" when
// idle, then "working", "done" or "failed" once the user installs an update.
// Reason is a stable code ("readonly", "permission", ...) the frontend turns
// into a localized message; Error carries the raw detail.
type UpdateView struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	// URL is the release page, used when in-app installation is unavailable.
	URL    string `json:"url"`
	State  string `json:"state"`
	Step   string `json:"step"`
	Reason string `json:"reason"`
	Error  string `json:"error"`
}

// updateController owns the check/throttle state and the install state
// machine. All network access goes through Source and Fetcher, which tests
// replace.
type updateController struct {
	current  string
	platform update.Platform
	source   update.Source
	fetcher  update.Fetcher
	store    *settings.Store
	now      func() time.Time
	emit     func(UpdateView)
	// newInstaller builds the platform installer for a release tag.
	newInstaller func(tag string) (update.Installer, error)
	// restart relaunches the app after a successful install.
	restart func() error

	mu        sync.Mutex
	inFlight  bool
	upgrading bool
	view      UpdateView
	result    update.Result
	lastCheck time.Time // in-memory fallback when settings are unwritable
}

func newUpdateController(current string, platform update.Platform, source update.Source, fetcher update.Fetcher, store *settings.Store) *updateController {
	return &updateController{
		current:      current,
		platform:     platform,
		source:       source,
		fetcher:      fetcher,
		store:        store,
		now:          time.Now,
		newInstaller: platformInstallerFor,
	}
}

// View returns the last check result.
func (c *updateController) View() UpdateView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view
}

func (c *updateController) setStore(st *settings.Store) {
	c.mu.Lock()
	c.store = st
	c.mu.Unlock()
}

func (c *updateController) setEmit(fn func(UpdateView)) {
	c.mu.Lock()
	c.emit = fn
	c.mu.Unlock()
}

func (c *updateController) setRestart(fn func() error) {
	c.mu.Lock()
	c.restart = fn
	c.mu.Unlock()
}

// Check looks for a newer release. force skips the 24-hour throttle and the
// "check on startup" setting; manual checks pass force=true. Errors are
// returned for manual checks; the automatic caller ignores them.
func (c *updateController) Check(ctx context.Context, force bool) (UpdateView, error) {
	c.mu.Lock()
	if c.inFlight || c.upgrading {
		c.mu.Unlock()
		return UpdateView{}, errCheckInFlight
	}
	// The new bundle is already on disk and only a restart is missing;
	// asking GitHub again would just re-offer the version we installed.
	if c.view.State == stateDone {
		view := c.view
		c.mu.Unlock()
		return view, nil
	}
	store := c.store
	last := c.lastCheck
	if store != nil {
		if persisted := store.LastUpdateCheck(); persisted.After(last) {
			last = persisted
		}
	}
	if !force {
		if store != nil && store.DisableUpdateCheck() {
			view := c.view
			c.mu.Unlock()
			return view, nil
		}
		if !last.IsZero() && c.now().Sub(last) < checkInterval {
			view := c.view
			c.mu.Unlock()
			return view, nil
		}
	}
	c.inFlight = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFlight = false
		c.mu.Unlock()
	}()

	res, err := update.CheckGUI(ctx, c.source, c.current, c.platform)
	// A non-release build never touched the network, so it must leave no
	// trace. Every real attempt counts, including failures: an offline
	// machine must not retry on every start. The in-memory stamp keeps the
	// throttle working even when the settings file is read-only.
	if !errors.Is(err, update.ErrNotReleaseBuild) {
		now := c.now()
		c.mu.Lock()
		c.lastCheck = now
		c.mu.Unlock()
		if store != nil {
			_ = store.RecordUpdateCheck(now)
		}
	}
	if err != nil {
		return c.View(), err
	}
	view := UpdateView{Available: res.Available, Current: res.Current, Latest: res.Latest}
	if res.Available {
		view.URL = releasePage + res.Latest
	}

	c.mu.Lock()
	c.view = view
	c.result = res
	emit := c.emit
	c.mu.Unlock()
	if emit != nil && view.Available {
		emit(view)
	}
	return view, nil
}

const (
	stateWorking = "working"
	stateDone    = "done"
	stateFailed  = "failed"
)

// StartUpgrade downloads, verifies and installs the available release. It
// reports progress through the emit callback. Expected failures (read-only
// location, permissions) are reflected in the returned view rather than as
// errors, so the banner can offer the download page; the error return is for
// callers that asked at the wrong time.
func (c *updateController) StartUpgrade(ctx context.Context) (UpdateView, error) {
	c.mu.Lock()
	if c.inFlight || c.upgrading {
		c.mu.Unlock()
		return UpdateView{}, errCheckInFlight
	}
	if c.view.State == stateDone {
		view := c.view
		c.mu.Unlock()
		return view, nil
	}
	if !c.view.Available {
		c.mu.Unlock()
		return UpdateView{}, errors.New("no update available")
	}
	res := c.result
	c.upgrading = true
	c.view.State = stateWorking
	c.view.Step = update.StageDownload
	c.view.Reason = ""
	c.view.Error = ""
	view := c.view
	emit := c.emit
	c.mu.Unlock()
	if emit != nil {
		emit(view)
	}

	inst, err := c.newInstaller(res.Latest)
	if err == nil {
		up := &update.Upgrader{
			Source:    c.source,
			Fetcher:   c.fetcher,
			Installer: inst,
			Current:   c.current,
			Platform:  c.platform,
			OnStep:    c.reportStep,
		}
		err = up.Apply(ctx, res)
	}

	c.mu.Lock()
	c.upgrading = false
	if err != nil {
		c.view.State = stateFailed
		c.view.Reason = failureReason(err)
		c.view.Error = err.Error()
	} else {
		c.view.State = stateDone
	}
	view = c.view
	emit = c.emit
	c.mu.Unlock()
	if emit != nil {
		emit(view)
	}
	return view, nil
}

// Restart relaunches the app after a successful install. The caller should
// exit the current process once it returns.
func (c *updateController) Restart() error {
	c.mu.Lock()
	done := c.view.State == stateDone
	restart := c.restart
	c.mu.Unlock()
	if !done {
		return errors.New("no installed update to restart into")
	}
	if restart == nil {
		return errors.New("restart is unavailable")
	}
	return restart()
}

// reportStep pushes a coarse progress update while an install is running.
func (c *updateController) reportStep(step string) {
	c.mu.Lock()
	if c.view.State != stateWorking {
		c.mu.Unlock()
		return
	}
	c.view.Step = step
	view := c.view
	emit := c.emit
	c.mu.Unlock()
	if emit != nil {
		emit(view)
	}
}

func failureReason(err error) string {
	switch {
	case errors.Is(err, update.ErrReadOnlyLocation):
		return "readonly"
	case errors.Is(err, update.ErrBundleNotWritable):
		return "permission"
	case errors.Is(err, update.ErrNotAppBundle), errors.Is(err, errUnsupportedPlatform):
		return "unsupported"
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	default:
		return "other"
	}
}

// platformInstallerFor builds the installer for the running app. It refuses
// before downloading anything when the install target cannot be replaced, so
// the user gets a drag-to-Applications or permission message immediately.
func platformInstallerFor(tag string) (update.Installer, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate executable: %w", err)
	}
	return installerFor(runtime.GOOS, exe, tag)
}

// installerFor is the platform switch behind platformInstallerFor, split out
// so each branch can be tested on one machine.
func installerFor(goos, exe, tag string) (update.Installer, error) {
	switch goos {
	case "darwin":
		bundle, err := update.LocateBundle(exe)
		if err != nil {
			return nil, err
		}
		if err := update.CheckInstallable(bundle); err != nil {
			return nil, err
		}
		return &update.BundleInstaller{Bundle: bundle, ExpectedVersion: tag}, nil
	case "windows":
		// Replace the real file, never a shim that launches it. Stage()
		// writes beside it, so an unwritable directory fails before the
		// download starts.
		resolved, err := filepath.EvalSymlinks(exe)
		if err != nil {
			return nil, fmt.Errorf("locate executable: %w", err)
		}
		return &update.BinaryInstaller{Target: resolved}, nil
	default:
		return nil, errUnsupportedPlatform
	}
}
