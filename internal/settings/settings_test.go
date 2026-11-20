package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestRecordInputSwitchMostRecentFirst(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	for _, in := range []string{"dp1", "hdmi2", "dp1"} {
		if err := st.RecordInputSwitch("m", in); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"dp1", "hdmi2"}
	if got := st.RecentInputs("m"); !slices.Equal(got, want) {
		t.Errorf("in memory: got %v, want %v", got, want)
	}
	if got := open(t, dir).RecentInputs("m"); !slices.Equal(got, want) {
		t.Errorf("persisted: got %v, want %v", got, want)
	}
}

func TestRecordInputSwitchCapsHistory(t *testing.T) {
	st := open(t, t.TempDir())
	for i := 0; i < maxRecent+3; i++ {
		if err := st.RecordInputSwitch("m", fmt.Sprintf("in%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	got := st.RecentInputs("m")
	if len(got) != maxRecent {
		t.Errorf("kept %d entries, want %d", len(got), maxRecent)
	}
	if got[0] != fmt.Sprintf("in%d", maxRecent+2) {
		t.Errorf("most recent entry is %q", got[0])
	}
}

func TestRecentKeptWhenLabelsRemoved(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	if err := st.SetLabel("m", "dp1", "Mac"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordInputSwitch("m", "hdmi2"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLabel("m", "dp1", ""); err != nil {
		t.Fatal(err)
	}
	re := open(t, dir)
	if got := re.RecentInputs("m"); !slices.Equal(got, []string{"hdmi2"}) {
		t.Errorf("recent history lost with the label: %v", got)
	}
	if got := re.Label("m", "dp1"); got != "" {
		t.Errorf("label should be gone, got %q", got)
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

// The current/previous input source is never persisted; only the order in
// which this app switched inputs is.
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

func TestUpdatePreferencesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := open(t, dir)
	if st.DisableUpdateCheck() {
		t.Error("update check should be enabled by default")
	}
	if got := st.LastUpdateCheck(); !got.IsZero() {
		t.Errorf("LastUpdateCheck = %v, want zero (never checked)", got)
	}

	when := time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)
	if err := st.SetDisableUpdateCheck(true); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordUpdateCheck(when); err != nil {
		t.Fatal(err)
	}

	re := open(t, dir)
	if !re.DisableUpdateCheck() {
		t.Error("disableUpdateCheck not persisted")
	}
	if got := re.LastUpdateCheck(); !got.Equal(when) {
		t.Errorf("LastUpdateCheck = %v, want %v", got, when)
	}
	snap := re.Snapshot()
	if !snap.DisableUpdateCheck {
		t.Error("Snapshot lost DisableUpdateCheck")
	}
	if snap.LastUpdateCheck != when.Format(time.RFC3339) {
		t.Errorf("Snapshot LastUpdateCheck = %q, want %q", snap.LastUpdateCheck, when.Format(time.RFC3339))
	}
}

func TestMissingUpdateFieldsStayDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"skipSwitchConfirm":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st := open(t, dir)
	if st.DisableUpdateCheck() {
		t.Error("missing disableUpdateCheck must mean enabled")
	}
	if got := st.LastUpdateCheck(); !got.IsZero() {
		t.Errorf("missing lastUpdateCheck = %v, want zero", got)
	}
}
