package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func open(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return st
}

func TestMonitorID(t *testing.T) {
	cases := map[string]string{
		"  Legion 27Q-1p ": "Legion 27Q-1p",
		"Odyssey\tG95NC":   "Odyssey G95NC",
		"":                 "",
	}
	for in, want := range cases {
		if got := MonitorID(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestMissingFileYieldsDefaultsAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	if st.SkipSwitchConfirm() || st.Label("m", "dp1") != "" {
		t.Error("want empty defaults")
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Error("open must not create the file")
	}
}

func TestCorruptFileYieldsDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := open(t, dir)
	if st.SkipSwitchConfirm() || len(st.Snapshot().Monitors) != 0 {
		t.Error("want defaults from a corrupt file")
	}
	// A later write must recover the file.
	if err := st.SetLabel("m", "dp1", "Mac"); err != nil {
		t.Fatal(err)
	}
	if got := open(t, dir).Label("m", "dp1"); got != "Mac" {
		t.Errorf("after recovery: got %q", got)
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	if err := st.SetLabel("G95NC", "dp1", "Mac mini"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLabel("G95NC", "hdmi2", " 工作PC "); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSkipSwitchConfirm(true); err != nil {
		t.Fatal(err)
	}
	re := open(t, dir)
	if got := re.Label("G95NC", "dp1"); got != "Mac mini" {
		t.Errorf("dp1: %q", got)
	}
	if got := re.Label("G95NC", "hdmi2"); got != "工作PC" {
		t.Errorf("hdmi2: %q (labels should be trimmed)", got)
	}
	if !re.SkipSwitchConfirm() {
		t.Error("skipSwitchConfirm not persisted")
	}
}

func TestMultipleMonitors(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	st.SetLabel("A", "dp1", "one")
	st.SetLabel("B", "dp1", "two")
	re := open(t, dir)
	if re.Label("A", "dp1") != "one" || re.Label("B", "dp1") != "two" {
		t.Error("monitors must not share labels")
	}
}

func TestEmptyLabelRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	st.SetLabel("A", "dp1", "one")
	st.SetLabel("A", "hdmi2", "two")
	if err := st.SetLabel("A", "dp1", "   "); err != nil {
		t.Fatal(err)
	}
	if got := st.Label("A", "dp1"); got != "" {
		t.Errorf("label should be gone, got %q", got)
	}
	st.SetLabel("A", "hdmi2", "")
	snap := st.Snapshot()
	if _, ok := snap.Monitors["A"]; ok {
		t.Error("monitor with no labels should be pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, fileName))
	if strings.Contains(string(data), `"monitors"`) {
		t.Errorf("pruned monitor should not be serialised: %s", data)
	}
}

func TestSetLabelRequiresKeys(t *testing.T) {
	st := open(t, t.TempDir())
	if err := st.SetLabel("", "dp1", "x"); err == nil {
		t.Error("empty monitor ID must fail")
	}
	if err := st.SetLabel("m", "", "x"); err == nil {
		t.Error("empty input must fail")
	}
}

func TestConcurrentWritesDoNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := st.SetLabel("m", fmt.Sprintf("in%02d", i), fmt.Sprintf("n%d", i)); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write: %v", err)
	}
	re := open(t, dir)
	for i := 0; i < n; i++ {
		in := fmt.Sprintf("in%02d", i)
		if got := re.Label("m", in); got != fmt.Sprintf("n%d", i) {
			t.Errorf("%s: got %q", in, got)
		}
	}
}

func TestNoTempFilesLeftBehind(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	for i := 0; i < 5; i++ {
		if err := st.SetLabel("m", "dp1", fmt.Sprintf("v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != fileName {
		names := []string{}
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("want only %s, got %v", fileName, names)
	}
}

// The old hotkey config lived next to the new file; it must never be read
// or rewritten.
func TestLegacyConfigUntouched(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "config.json")
	const content = `{"hotkeys":[{"keys":"ctrl+alt+up","action":"brightness"}]}`
	if err := os.WriteFile(legacy, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st := open(t, dir)
	st.SetLabel("m", "dp1", "x")
	st.SetSkipSwitchConfirm(true)
	got, err := os.ReadFile(legacy)
	if err != nil || string(got) != content {
		t.Errorf("legacy config changed: %q, %v", got, err)
	}
}

// No behavioural state such as a previous input source may be persisted.
func TestNoBehaviouralStatePersisted(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	st.SetLabel("m", "dp1", "x")
	st.SetSkipSwitchConfirm(true)
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"previous", "lastInput", "current", "native", "hotkey"} {
		if strings.Contains(string(data), banned) {
			t.Errorf("settings file contains %q: %s", banned, data)
		}
	}
}
