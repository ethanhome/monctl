package main

import (
	"errors"
	"testing"

	"github.com/bacnh85/monctl/internal/service"
	"github.com/bacnh85/monctl/monitor"
)

type fakeCtrl struct {
	infos  []monitor.Info
	cur    map[byte]int
	max    map[byte]int
	writes map[byte]byte
	getErr error
}

func newFake() *fakeCtrl {
	return &fakeCtrl{
		infos: []monitor.Info{{Index: 0, Description: "Test Panel"}, {Index: 1, Description: "Second"}},
		cur: map[byte]int{
			monitor.VCPInput:      0x1B, // unknown to the name table
			monitor.VCPBrightness: 30,   // of max 50 -> 60%
			monitor.VCPContrast:   25,   // of max 50 -> 50%
			monitor.VCPPower:      0,    // unreported
		},
		max:    map[byte]int{monitor.VCPBrightness: 50, monitor.VCPContrast: 50},
		writes: map[byte]byte{},
	}
}

func (f *fakeCtrl) List() ([]monitor.Info, error) { return f.infos, nil }
func (f *fakeCtrl) GetVCP(_ monitor.Info, code byte) (int, int, error) {
	if f.getErr != nil {
		return 0, 0, f.getErr
	}
	return f.cur[code], f.max[code], nil
}
func (f *fakeCtrl) SetVCP(_ monitor.Info, code, v byte) error { f.writes[code] = v; return nil }

func newTestApp(f *fakeCtrl) *App { return newApp(service.NewWithController(f)) }

func TestRunIfLatestDropsStaleGeneration(t *testing.T) {
	a := newTestApp(newFake())
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
	a := newTestApp(f)
	if err := a.SetBrightness(0, 60); err != nil {
		t.Fatal(err)
	}
	if got := f.writes[monitor.VCPBrightness]; got != 30 {
		t.Errorf("wrote %d, want 30 (60%% of max 50)", got)
	}
}

func TestSetLevelRejectsOutOfRange(t *testing.T) {
	f := newFake()
	a := newTestApp(f)
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

func TestSetPower(t *testing.T) {
	f := newFake()
	a := newTestApp(f)
	for state, want := range map[string]byte{"on": monitor.PowerOn, "off": monitor.PowerOff, "standby": monitor.PowerStandby} {
		if err := a.SetPower(0, state); err != nil {
			t.Fatal(err)
		}
		if got := f.writes[monitor.VCPPower]; got != want {
			t.Errorf("%s: wrote 0x%02X want 0x%02X", state, got, want)
		}
	}
	if err := a.SetPower(0, "sleep"); err == nil {
		t.Error("unknown state must fail")
	}
}

// Monitor index 0 is a real monitor; it must not mean "all".
func TestSetInputTargetsIndexZero(t *testing.T) {
	f := newFake()
	if err := newTestApp(f).SetInput(0, "hdmi2"); err != nil {
		t.Fatal(err)
	}
	if got := f.writes[monitor.VCPInput]; got != 0x12 {
		t.Errorf("wrote 0x%02X, want 0x12", got)
	}
}

func TestListMonitorsMapsReadings(t *testing.T) {
	f := newFake()
	got, err := newTestApp(f).ListMonitors()
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
	if m.Power.OK {
		t.Errorf("power 0/0 must not be reported as a known state: %+v", m.Power)
	}
}

func TestListMonitorsMarksUnreadableControls(t *testing.T) {
	f := newFake()
	f.getErr = errors.New("ddc down")
	got, err := newTestApp(f).ListMonitors()
	if err != nil || len(got) != 2 {
		t.Fatalf("a DDC read failure must not fail the whole listing: %v", err)
	}
	for _, m := range got {
		if m.Input.OK || m.Brightness.OK || m.Contrast.OK || m.Power.OK {
			t.Errorf("all controls should be unknown: %+v", m)
		}
	}
}
