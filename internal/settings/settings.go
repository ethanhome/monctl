// Package settings persists GUI-only preferences: per-monitor input-source
// labels, recent switch order, and whether the input-switch confirmation
// prompt is skipped.
//
// It is deliberately separate from the removed hotkey config.json. The
// current input source is always read from the monitor itself; only the
// order in which this app switched inputs is stored.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const fileName = "settings.json"

// Settings is the on-disk document.
type Settings struct {
	SkipSwitchConfirm bool               `json:"skipSwitchConfirm"`
	Monitors          map[string]Monitor `json:"monitors,omitempty"`
}

// Monitor holds per-monitor display preferences.
type Monitor struct {
	// Labels maps input-source names ("dp1", "hdmi2") to user-chosen names.
	Labels map[string]string `json:"labels,omitempty"`
	// Recent lists input-source names most recently switched to first.
	Recent []string `json:"recent,omitempty"`
}

// maxRecent bounds the stored switch history; the GUI has seven fixed
// sources, so this only guards against hand-edited settings.
const maxRecent = 8

// MonitorID derives the settings key for a monitor. monitor.Info has no
// serial number, so the EDID description (model) is the only stable
// identifier; two identical models therefore share one entry.
func MonitorID(description string) string {
	return strings.Join(strings.Fields(description), " ")
}

// DefaultPath returns os.UserConfigDir()/monctl/settings.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "monctl", fileName), nil
}

// Store is a settings file guarded by a mutex. Every mutation rewrites the
// file atomically before the in-memory copy changes, so concurrent callers
// cannot lose updates and a failed write leaves memory untouched.
type Store struct {
	path string
	mu   sync.Mutex
	s    Settings
}

// Open loads the settings at path. A missing or corrupt file yields defaults
// and no error; only unreadable files (e.g. permissions) are reported.
func Open(path string) (*Store, error) {
	st := &Store{path: path, s: Settings{Monitors: map[string]Monitor{}}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return st, nil // corrupt: fall back to defaults
	}
	if s.Monitors == nil {
		s.Monitors = map[string]Monitor{}
	}
	st.s = s
	return st, nil
}

// Path returns the file backing the store.
func (st *Store) Path() string { return st.path }

// Snapshot returns a deep copy of the current settings.
func (st *Store) Snapshot() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.clone()
}

// Label returns the user-chosen name for monitorID's input, or "".
func (st *Store) Label(monitorID, input string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.Monitors[monitorID].Labels[input]
}

// RecentInputs returns monitorID's inputs with the most recently switched
// first. The slice is a copy the caller may keep.
func (st *Store) RecentInputs(monitorID string) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]string(nil), st.s.Monitors[monitorID].Recent...)
}

// SkipSwitchConfirm reports whether the switch confirmation prompt is off.
func (st *Store) SkipSwitchConfirm() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.SkipSwitchConfirm
}

// SetLabel stores name for monitorID's input; an empty name removes the
// label, and a monitor left with neither labels nor switch history is
// dropped from the file.
func (st *Store) SetLabel(monitorID, input, name string) error {
	if monitorID == "" || input == "" {
		return errors.New("settings: monitor and input are required")
	}
	name = strings.TrimSpace(name)
	return st.update(func(s *Settings) {
		m := s.Monitors[monitorID]
		if m.Labels == nil {
			m.Labels = map[string]string{}
		}
		if name == "" {
			delete(m.Labels, input)
		} else {
			m.Labels[input] = name
		}
		if len(m.Labels) == 0 && len(m.Recent) == 0 {
			delete(s.Monitors, monitorID)
		} else {
			s.Monitors[monitorID] = m
		}
	})
}

// RecordInputSwitch moves input to the front of monitorID's recent list,
// dropping any older occurrence so the list stays deduplicated.
func (st *Store) RecordInputSwitch(monitorID, input string) error {
	if monitorID == "" || input == "" {
		return errors.New("settings: monitor and input are required")
	}
	return st.update(func(s *Settings) {
		m := s.Monitors[monitorID]
		next := make([]string, 0, len(m.Recent)+1)
		next = append(next, input)
		for _, name := range m.Recent {
			if name != input {
				next = append(next, name)
			}
		}
		if len(next) > maxRecent {
			next = next[:maxRecent]
		}
		m.Recent = next
		s.Monitors[monitorID] = m
	})
}

// SetSkipSwitchConfirm toggles the switch confirmation prompt.
func (st *Store) SetSkipSwitchConfirm(skip bool) error {
	return st.update(func(s *Settings) { s.SkipSwitchConfirm = skip })
}

// update applies fn to a copy, writes it, and only then commits it in memory.
func (st *Store) update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	next := st.s.clone()
	fn(&next)
	if err := writeAtomic(st.path, next); err != nil {
		return err
	}
	st.s = next
	return nil
}

// writeAtomic writes s to path via a temp file in the same directory plus a
// rename, so a crash or full disk cannot leave a half-written settings file.
func writeAtomic(path string, s Settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s Settings) clone() Settings {
	out := Settings{SkipSwitchConfirm: s.SkipSwitchConfirm, Monitors: make(map[string]Monitor, len(s.Monitors))}
	for id, m := range s.Monitors {
		labels := make(map[string]string, len(m.Labels))
		for k, v := range m.Labels {
			labels[k] = v
		}
		out.Monitors[id] = Monitor{Labels: labels, Recent: append([]string(nil), m.Recent...)}
	}
	return out
}
