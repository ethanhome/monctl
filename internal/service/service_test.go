package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/bacnh85/monctl/monitor"
)

type fakeCtrl struct {
	infos  []monitor.Info
	cur    map[byte]int
	max    map[byte]int
	getErr error
	writes map[byte]byte
}

func newFake(max int) *fakeCtrl {
	return &fakeCtrl{
		infos:  []monitor.Info{{Index: 0}, {Index: 1}},
		cur:    map[byte]int{monitor.VCPBrightness: 20, monitor.VCPInput: 0x0F, monitor.VCPPower: 1},
		max:    map[byte]int{monitor.VCPBrightness: max, monitor.VCPContrast: max},
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

func TestLookupControl(t *testing.T) {
	c, err := LookupControl("Brightness")
	if err != nil || c.Name != "brightness" || c.Code != monitor.VCPBrightness || !c.Continuous() {
		t.Fatalf("got %+v, %v", c, err)
	}
	if c, _ := LookupControl("input"); c.Continuous() {
		t.Error("input must not be continuous")
	}
	if _, err := LookupControl("Volume"); err == nil || !strings.Contains(err.Error(), `"volume"`) {
		t.Errorf("want unknown-control error naming volume, got %v", err)
	}
}

func TestSetContinuousScalesToMax(t *testing.T) {
	cases := []struct {
		raw     string
		max     int
		want    byte
		wantPct int
	}{
		{"30", 50, 15, 30}, // G95NC reports max=50
		{"100", 50, 50, 100},
		{"0", 50, 0, 0},
		{"60", 0, 60, 60}, // max unknown -> assume 100
	}
	for _, tc := range cases {
		f := newFake(tc.max)
		pct, err := NewWithController(f).Set(monitor.Info{}, Control{"brightness", monitor.VCPBrightness}, tc.raw)
		if err != nil || pct != tc.wantPct || f.writes[monitor.VCPBrightness] != tc.want {
			t.Errorf("set %s max=%d: pct=%d wrote=%d err=%v; want pct=%d wrote=%d",
				tc.raw, tc.max, pct, f.writes[monitor.VCPBrightness], err, tc.wantPct, tc.want)
		}
	}
}

func TestSetContinuousOutOfRange(t *testing.T) {
	f := newFake(50)
	_, err := NewWithController(f).Set(monitor.Info{}, Control{"brightness", monitor.VCPBrightness}, "101")
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("want out of range, got %v", err)
	}
	if len(f.writes) != 0 {
		t.Error("nothing should be written")
	}
}

func TestSetOffset(t *testing.T) {
	cases := []struct {
		raw  string
		cur  int
		max  int
		want byte
	}{
		{"+10", 20, 50, 25}, // +10 percent points of 50 = +5
		{"-10", 20, 50, 15},
		{"+100", 20, 50, 50}, // clamp high
		{"-100", 20, 50, 0},  // clamp low
		{"+10", 20, 0, 30},   // max unknown -> 100
	}
	for _, tc := range cases {
		f := newFake(tc.max)
		f.cur[monitor.VCPBrightness] = tc.cur
		_, err := NewWithController(f).Set(monitor.Info{}, Control{"brightness", monitor.VCPBrightness}, tc.raw)
		if err != nil || f.writes[monitor.VCPBrightness] != tc.want {
			t.Errorf("%s cur=%d max=%d: wrote=%d err=%v; want %d", tc.raw, tc.cur, tc.max, f.writes[monitor.VCPBrightness], err, tc.want)
		}
	}
}

func TestSetBadOffset(t *testing.T) {
	_, err := NewWithController(newFake(50)).Set(monitor.Info{}, Control{"brightness", monitor.VCPBrightness}, "+x")
	if err == nil || !strings.Contains(err.Error(), "bad offset") {
		t.Fatalf("got %v", err)
	}
}

func TestSetInputAndPower(t *testing.T) {
	f := newFake(50)
	s := NewWithController(f)
	for _, tc := range []struct {
		ctl  Control
		raw  string
		want byte
	}{
		{Control{"input", monitor.VCPInput}, "hdmi2", 0x12},
		{Control{"input", monitor.VCPInput}, "0x1B", 0x1B},
		{Control{"power", monitor.VCPPower}, "off", monitor.PowerOff},
		{Control{"power", monitor.VCPPower}, "standby", monitor.PowerStandby},
	} {
		if _, err := s.Set(monitor.Info{}, tc.ctl, tc.raw); err != nil || f.writes[tc.ctl.Code] != tc.want {
			t.Errorf("%s %s: wrote=0x%02X err=%v; want 0x%02X", tc.ctl.Name, tc.raw, f.writes[tc.ctl.Code], err, tc.want)
		}
	}
	if _, err := s.Set(monitor.Info{}, Control{"input", monitor.VCPInput}, "bogus"); err == nil {
		t.Error("bogus input must fail")
	}
}

func TestSetPropagatesReadError(t *testing.T) {
	f := newFake(50)
	f.getErr = errors.New("ddc down")
	s := NewWithController(f)
	for _, raw := range []string{"+5", "50"} {
		if _, err := s.Set(monitor.Info{}, Control{"brightness", monitor.VCPBrightness}, raw); err == nil {
			t.Errorf("%s: want error", raw)
		}
	}
	if len(f.writes) != 0 {
		t.Error("nothing should be written")
	}
}

func TestScanKeepsPerReadingErrors(t *testing.T) {
	f := newFake(50)
	got, err := NewWithController(f).Scan()
	if err != nil || len(got) != 2 || got[0].Input.Cur != 0x0F || got[0].Power.Cur != 1 || got[0].Input.Err != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	f.getErr = errors.New("ddc down")
	got, _ = NewWithController(f).Scan()
	if got[0].Input.Err == nil {
		t.Error("read error must be kept on the reading, not dropped")
	}
}

func TestTargets(t *testing.T) {
	s := NewWithController(newFake(50))
	if all, err := s.Targets(0); err != nil || len(all) != 2 {
		t.Errorf("all: %v %v", all, err)
	}
	if one, err := s.Targets(1); err != nil || len(one) != 1 || one[0].Index != 1 {
		t.Errorf("one: %v %v", one, err)
	}
	if _, err := s.Targets(9); err == nil {
		t.Error("missing index must fail")
	}
}

func TestByIndexIncludesZero(t *testing.T) {
	s := NewWithController(newFake(50))
	m, err := s.ByIndex(0)
	if err != nil || m.Index != 0 {
		t.Fatalf("index 0 must resolve to a single monitor, got %+v, %v", m, err)
	}
	if m, err := s.ByIndex(1); err != nil || m.Index != 1 {
		t.Fatalf("index 1: %+v, %v", m, err)
	}
	if _, err := s.ByIndex(9); err == nil {
		t.Error("missing index must fail")
	}
}

func TestParseHexOrDec(t *testing.T) {
	cases := []struct {
		in      string
		want    byte
		wantErr bool
	}{
		{"0x12", 0x12, false},
		{"0X12", 0x12, false},
		{"12", 0x12, false}, // hex first: matches historical behaviour
		{"ff", 0xFF, false},
		{"0x100", 0, true},
		{"zz", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseHexOrDec(tc.in)
		if (err != nil) != tc.wantErr || (!tc.wantErr && got != tc.want) {
			t.Errorf("%q: got 0x%02X err=%v", tc.in, got, err)
		}
	}
}

func TestPowerName(t *testing.T) {
	for v, want := range map[byte]string{1: "on", 4: "off", 5: "standby", 0x7: "0x07"} {
		if got := PowerName(v); got != want {
			t.Errorf("%d: got %q want %q", v, got, want)
		}
	}
}
