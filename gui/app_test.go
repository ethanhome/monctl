package main

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bacnh85/monctl/internal/service"
	"github.com/bacnh85/monctl/internal/settings"
	"github.com/bacnh85/monctl/monitor"
)

type fakeCtrl struct {
	infos  []monitor.Info
	cur    map[byte]int
	max    map[byte]int
	writes map[byte]byte
	reads  map[byte]int
	getErr error
	setErr error
}

func newFake() *fakeCtrl {
	return &fakeCtrl{
		infos: []monitor.Info{{Index: 0, Description: "Test Panel"}, {Index: 1, Description: "Second"}},
		cur: map[byte]int{
			monitor.VCPInput:      0x1B, // unknown to the name table
			monitor.VCPBrightness: 30,   // of max 50 -> 60%
			monitor.VCPContrast:   25,   // of max 50 -> 50%
		},
		max:    map[byte]int{monitor.VCPBrightness: 50, monitor.VCPContrast: 50},
		writes: map[byte]byte{},
		reads:  map[byte]int{},
	}
}

func (f *fakeCtrl) List() ([]monitor.Info, error) { return f.infos, nil }
func (f *fakeCtrl) GetVCP(_ monitor.Info, code byte) (int, int, error) {
	f.reads[code]++
	if f.getErr != nil {
		return 0, 0, f.getErr
	}
	return f.cur[code], f.max[code], nil
}
func (f *fakeCtrl) SetVCP(_ monitor.Info, code, v byte) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.writes[code] = v
	return nil
}

// newTestApp points the app at a throwaway settings file so tests never
// touch the user's real config.
func newTestApp(t *testing.T, f *fakeCtrl) *App {
	t.Helper()
	a := newApp(service.NewWithController(f))
	st, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.st = st
	return a
}

func TestRunIfLatestDropsStaleGeneration(t *testing.T) {
	a := newTestApp(t, newFake())
	const key = "0:brightness"
	a.seqMu.Lock()
	a.seq[key]++
	g1 := a.seq[key]
	a.seq[key]++
	g2 := a.seq[key]
	a.seqMu.Unlock()

	calls := 0
	fn := func() error { calls++; return nil }
	if err := a.runIfLatest(key, g1, fn); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Error("stale write must be dropped")
	}
	if err := a.runIfLatest(key, g2, fn); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Error("latest write must run")
	}
}

func TestSetBrightnessScalesToPanelMax(t *testing.T) {
	f := newFake()
	a := newTestApp(t, f)
	if err := a.SetBrightness(0, 60); err != nil {
		t.Fatal(err)
	}
	if got := f.writes[monitor.VCPBrightness]; got != 30 {
		t.Errorf("wrote %d, want 30 (60%% of max 50)", got)
	}
}

func TestSetLevelRejectsOutOfRange(t *testing.T) {
	f := newFake()
	a := newTestApp(t, f)
	for _, pct := range []int{-1, 101} {
		if err := a.SetBrightness(0, pct); err == nil {
			t.Errorf("%d: want error", pct)
		}
		if err := a.SetContrast(0, pct); err == nil {
			t.Errorf("contrast %d: want error", pct)
		}
	}
	if len(f.writes) != 0 {
		t.Error("nothing should be written")
	}
}

// Monitor index 0 is a real monitor; it must not mean "all".
func TestSetInputTargetsIndexZero(t *testing.T) {
	f := newFake()
	if err := newTestApp(t, f).SetInput(0, "hdmi2"); err != nil {
		t.Fatal(err)
	}
	if got := f.writes[monitor.VCPInput]; got != 0x12 {
		t.Errorf("wrote 0x%02X, want 0x12", got)
	}
}

func TestListMonitorsMapsReadings(t *testing.T) {
	f := newFake()
	got, err := newTestApp(t, f).ListMonitors()
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	m := got[0]
	if m.Index != 0 || m.ID != "Test Panel" {
		t.Errorf("identity: %+v", m)
	}
	if !m.Input.OK || m.Input.Name != "" || m.Input.Raw != "0x1B" {
		t.Errorf("unknown input value: %+v", m.Input)
	}
	if !m.Brightness.OK || m.Brightness.Percent != 60 {
		t.Errorf("brightness: %+v", m.Brightness)
	}
	if !m.Contrast.OK || m.Contrast.Percent != 50 {
		t.Errorf("contrast: %+v", m.Contrast)
	}
	// Power is CLI-only; the GUI must not touch VCP 0xD6.
	if f.reads[monitor.VCPPower] != 0 {
		t.Errorf("GUI read power %d times", f.reads[monitor.VCPPower])
	}
}

func TestListMonitorsMarksUnreadableControls(t *testing.T) {
	f := newFake()
	f.getErr = errors.New("ddc down")
	got, err := newTestApp(t, f).ListMonitors()
	if err != nil || len(got) != 2 {
		t.Fatalf("a DDC read failure must not fail the whole listing: %v", err)
	}
	for _, m := range got {
		if m.Input.OK || m.Brightness.OK || m.Contrast.OK {
			t.Errorf("all controls should be unknown: %+v", m)
		}
	}
}

func TestSetInputRecordsRecentOrder(t *testing.T) {
	f := newFake()
	a := newTestApp(t, f)
	for _, in := range []string{"hdmi2", "dp1", "hdmi2"} {
		if err := a.SetInput(0, in); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"hdmi2", "dp1"}
	if got := a.st.RecentInputs("Test Panel"); !slices.Equal(got, want) {
		t.Errorf("settings recent: got %v, want %v", got, want)
	}
	got, err := a.ListMonitors()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0].Recent, want) {
		t.Errorf("view recent: got %v, want %v", got[0].Recent, want)
	}
	if len(got[1].Recent) != 0 {
		t.Errorf("other monitor must not gain history: %v", got[1].Recent)
	}
}

// Raw VCP values must normalise to the same key the UI uses for the row.
func TestSetInputRecordsNormalisedKey(t *testing.T) {
	a := newTestApp(t, newFake())
	if err := a.SetInput(0, "0x12"); err != nil {
		t.Fatal(err)
	}
	if got := a.st.RecentInputs("Test Panel"); !slices.Equal(got, []string{"hdmi2"}) {
		t.Errorf("got %v, want [hdmi2]", got)
	}
}

func TestSetInputFailureDoesNotRecord(t *testing.T) {
	f := newFake()
	f.setErr = errors.New("ddc down")
	a := newTestApp(t, f)
	if err := a.SetInput(0, "hdmi2"); err == nil {
		t.Fatal("want error")
	}
	if got := a.st.RecentInputs("Test Panel"); len(got) != 0 {
		t.Errorf("failed switch must not be recorded: %v", got)
	}
}
