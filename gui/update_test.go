package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bacnh85/monctl/internal/settings"
	"github.com/bacnh85/monctl/internal/update"
)

type countingSource struct {
	rel   update.Release
	err   error
	calls int
}

func (s *countingSource) Latest(context.Context) (update.Release, error) {
	s.calls++
	return s.rel, s.err
}

const (
	guiZipURL = "https://example.test/monctl-macos-arm64.zip"
	sumsURL   = "https://example.test/SHA256SUMS"
)

func updateRelease() update.Release {
	return update.Release{
		Tag: "v0.3.0",
		Assets: []update.Asset{
			{Name: "monctl-macos-arm64.zip", URL: guiZipURL},
			{Name: "SHA256SUMS", URL: sumsURL},
		},
	}
}

func newTestController(t *testing.T, current string, src update.Source) (*updateController, *settings.Store, *time.Time) {
	t.Helper()
	st, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	c := newUpdateController(current, update.Platform{OS: "darwin", Arch: "arm64"}, src, nil, st)
	c.now = func() time.Time { return now }
	return c, st, &now
}

func TestCheckReportsAvailableRelease(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, st, _ := newTestController(t, "v0.2.0", src)
	var emitted []UpdateView
	c.emit = func(v UpdateView) { emitted = append(emitted, v) }

	view, err := c.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !view.Available || view.Current != "v0.2.0" || view.Latest != "v0.3.0" {
		t.Fatalf("view = %+v", view)
	}
	if want := "https://github.com/ethanhome/monctl/releases/tag/v0.3.0"; view.URL != want {
		t.Fatalf("URL = %q, want %q", view.URL, want)
	}
	if len(emitted) != 1 || emitted[0] != view {
		t.Fatalf("emitted = %+v, want the view", emitted)
	}
	if st.LastUpdateCheck().IsZero() {
		t.Fatal("check time was not recorded")
	}
}

func TestAutomaticCheckHonoursThrottle(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, _, now := newTestController(t, "v0.2.0", src)

	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 {
		t.Fatalf("calls = %d, want 1", src.calls)
	}

	*now = now.Add(time.Hour)
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 {
		t.Fatalf("calls within 24h = %d, want 1", src.calls)
	}

	*now = now.Add(25 * time.Hour)
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if src.calls != 2 {
		t.Fatalf("calls after 24h = %d, want 2", src.calls)
	}
}

func TestManualCheckBypassesThrottle(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, _, _ := newTestController(t, "v0.2.0", src)

	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if src.calls != 2 {
		t.Fatalf("calls = %d, want 2 (manual check must not be throttled)", src.calls)
	}
}

func TestAutomaticCheckRespectsDisabledSetting(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, st, _ := newTestController(t, "v0.2.0", src)
	if err := st.SetDisableUpdateCheck(true); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if src.calls != 0 {
		t.Fatalf("calls = %d, want 0 while disabled", src.calls)
	}
	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 {
		t.Fatalf("manual call = %d, want 1 even while disabled", src.calls)
	}
}

func TestCheckFailureIsReturnedWithoutEmitting(t *testing.T) {
	src := &countingSource{err: errors.New("network down")}
	c, st, _ := newTestController(t, "v0.2.0", src)
	var emitted int
	c.emit = func(UpdateView) { emitted++ }

	if _, err := c.Check(context.Background(), true); err == nil {
		t.Fatal("Check = nil error, want failure")
	}
	if emitted != 0 {
		t.Fatalf("emitted %d times, want 0", emitted)
	}
	if st.LastUpdateCheck().IsZero() {
		t.Fatal("failed attempt was not recorded; offline starts would retry every launch")
	}
}

func TestCheckRefusesDevBuild(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, st, _ := newTestController(t, "dev", src)

	_, err := c.Check(context.Background(), true)
	if !errors.Is(err, update.ErrNotReleaseBuild) {
		t.Fatalf("err = %v, want ErrNotReleaseBuild", err)
	}
	if src.calls != 0 {
		t.Fatalf("source called %d times, want 0", src.calls)
	}
	if !st.LastUpdateCheck().IsZero() {
		t.Fatal("dev build recorded a check time; it never touched the network")
	}
}

// A read-only settings file must not disable the throttle for this session.
func TestThrottleSurvivesUnwritableSettings(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permission semantics as a non-root user")
	}
	src := &countingSource{rel: updateRelease()}
	dir := t.TempDir()
	st, err := settings.Open(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	c := newUpdateController("v0.2.0", update.Platform{OS: "darwin", Arch: "arm64"}, src, nil, st)
	c.now = func() time.Time { return now }

	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatalf("first check: %v", err)
	}
	if !st.LastUpdateCheck().IsZero() {
		t.Fatal("settings were writable; test setup is wrong")
	}
	now = now.Add(time.Hour)
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatalf("second check: %v", err)
	}
	if src.calls != 1 {
		t.Fatalf("calls within 24h = %d, want 1 (in-memory throttle)", src.calls)
	}
}

type blockingSource struct {
	entered chan struct{}
	release chan struct{}
}

func (s *blockingSource) Latest(context.Context) (update.Release, error) {
	close(s.entered)
	<-s.release
	return updateRelease(), nil
}

func TestConcurrentCheckIsRejected(t *testing.T) {
	src := &blockingSource{entered: make(chan struct{}), release: make(chan struct{})}
	c, _, _ := newTestController(t, "v0.2.0", src)

	done := make(chan error, 1)
	go func() {
		_, err := c.Check(context.Background(), true)
		done <- err
	}()
	<-src.entered

	if _, err := c.Check(context.Background(), true); !errors.Is(err, errCheckInFlight) {
		t.Fatalf("second check err = %v, want errCheckInFlight", err)
	}
	close(src.release)
	if err := <-done; err != nil {
		t.Fatalf("first check: %v", err)
	}
}

type guiFetcher map[string]string

func (f guiFetcher) Get(_ context.Context, url string) (io.ReadCloser, error) {
	body, ok := f[url]
	if !ok {
		return nil, fmt.Errorf("unexpected URL %s", url)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type recordingInstaller struct {
	dir       string
	installed bool
	aborted   bool
}

func (r *recordingInstaller) Stage() (string, error) {
	f, err := os.CreateTemp(r.dir, "staged-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return "", err
	}
	return name, nil
}

func (r *recordingInstaller) Install(string) error {
	r.installed = true
	return nil
}

func (r *recordingInstaller) Abort(string) { r.aborted = true }

func TestStartUpgradeInstallsRelease(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, _, _ := newTestController(t, "v0.2.0", src)
	c.fetcher = guiFetcher{
		guiZipURL: "zip body",
		sumsURL:   sha256Hex("zip body") + "  monctl-macos-arm64.zip\n",
	}
	inst := &recordingInstaller{dir: t.TempDir()}
	c.newInstaller = func(tag string) (update.Installer, error) {
		if tag != "v0.3.0" {
			t.Errorf("installer built for %q, want v0.3.0", tag)
		}
		return inst, nil
	}
	var states []string
	c.setEmit(func(v UpdateView) { states = append(states, v.State) })

	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatalf("Check: %v", err)
	}
	view, err := c.StartUpgrade(context.Background())
	if err != nil {
		t.Fatalf("StartUpgrade: %v", err)
	}
	if !inst.installed {
		t.Fatal("installer was not called")
	}
	if view.State != stateDone || view.Latest != "v0.3.0" {
		t.Fatalf("view = %+v, want done at v0.3.0", view)
	}
	if got := strings.Join(states, ","); got != ",working,working,working,done" {
		t.Fatalf("emitted states = %q, want available,working,working,working,done", got)
	}

	restarted := false
	c.setRestart(func() error { restarted = true; return nil })
	if err := c.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !restarted {
		t.Fatal("restart hook was not called")
	}

	// The new bundle is already in place; a further check must not offer the
	// same version again before the restart.
	before := src.calls
	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatalf("Check after install: %v", err)
	}
	if src.calls != before {
		t.Fatal("checked the network after the update was installed")
	}
}

func TestStartUpgradeReportsReadOnlyLocation(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, _, _ := newTestController(t, "v0.2.0", src)
	c.newInstaller = func(string) (update.Installer, error) {
		return nil, fmt.Errorf("wrapped: %w", update.ErrReadOnlyLocation)
	}
	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatalf("Check: %v", err)
	}

	view, err := c.StartUpgrade(context.Background())
	if err != nil {
		t.Fatalf("StartUpgrade: %v", err)
	}
	if view.State != stateFailed || view.Reason != "readonly" {
		t.Fatalf("view = %+v, want failed/readonly", view)
	}
	if view.Error == "" {
		t.Fatal("failure detail is empty")
	}
	if err := c.Restart(); err == nil {
		t.Fatal("Restart succeeded after a failed install")
	}
}

func TestStartUpgradeReportsChecksumFailure(t *testing.T) {
	src := &countingSource{rel: updateRelease()}
	c, _, _ := newTestController(t, "v0.2.0", src)
	c.fetcher = guiFetcher{
		guiZipURL: "tampered",
		sumsURL:   sha256Hex("expected") + "  monctl-macos-arm64.zip\n",
	}
	inst := &recordingInstaller{dir: t.TempDir()}
	c.newInstaller = func(string) (update.Installer, error) { return inst, nil }
	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatalf("Check: %v", err)
	}

	view, err := c.StartUpgrade(context.Background())
	if err != nil {
		t.Fatalf("StartUpgrade: %v", err)
	}
	if view.State != stateFailed || view.Reason != "other" {
		t.Fatalf("view = %+v, want failed/other", view)
	}
	if inst.installed {
		t.Fatal("installer ran despite a checksum mismatch")
	}
}

func TestInstallerForWindowsTargetsRunningExe(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "monctl-gui-windows-amd64.exe")
	if err := os.WriteFile(exe, []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	inst, err := installerFor("windows", exe, "v0.3.0")
	if err != nil {
		t.Fatalf("installerFor: %v", err)
	}
	bi, ok := inst.(*update.BinaryInstaller)
	if !ok {
		t.Fatalf("installer = %T, want *update.BinaryInstaller", inst)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if bi.Target != want {
		t.Fatalf("target = %s, want %s", bi.Target, want)
	}
}

func TestInstallerForDarwinTargetsBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "monctl.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "monctl")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>` +
		`<key>CFBundleShortVersionString</key><string>0.2.0</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	inst, err := installerFor("darwin", exe, "v0.3.0")
	if err != nil {
		t.Fatalf("installerFor: %v", err)
	}
	bi, ok := inst.(*update.BundleInstaller)
	if !ok {
		t.Fatalf("installer = %T, want *update.BundleInstaller", inst)
	}
	want, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if bi.Bundle != want || bi.ExpectedVersion != "v0.3.0" {
		t.Fatalf("installer = %+v, want bundle %s at v0.3.0", bi, want)
	}
}

func TestInstallerForRejectsOtherPlatforms(t *testing.T) {
	if _, err := installerFor("linux", "/tmp/monctl", "v0.3.0"); !errors.Is(err, errUnsupportedPlatform) {
		t.Fatalf("err = %v, want errUnsupportedPlatform", err)
	}
}

func TestFailureReason(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("wrap: %w", update.ErrReadOnlyLocation), "readonly"},
		{update.ErrBundleNotWritable, "permission"},
		{fmt.Errorf("stage: %w", fs.ErrPermission), "permission"},
		{update.ErrNotAppBundle, "unsupported"},
		{errUnsupportedPlatform, "unsupported"},
		{errors.New("checksum mismatch"), "other"},
	}
	for _, tc := range cases {
		if got := failureReason(tc.err); got != tc.want {
			t.Fatalf("failureReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
