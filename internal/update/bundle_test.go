package update_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bacnh85/monctl/internal/update"
)

func plist(version string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>monctl</string>
	<key>CFBundleShortVersionString</key>
	<string>` + version + `</string>
</dict>
</plist>
`
}

// makeBundle creates a minimal app bundle whose executable is returned.
func makeBundle(t *testing.T, parent, version, marker string) (bundle, exe string) {
	t.Helper()
	bundle = filepath.Join(parent, "monctl.app")
	exe = filepath.Join(bundle, "Contents", "MacOS", "monctl")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bundle, "Contents", "Info.plist"), plist(version))
	writeFile(t, exe, marker)
	return bundle, exe
}

func TestBundleVersion(t *testing.T) {
	dir := t.TempDir()
	bundle, _ := makeBundle(t, dir, "0.3.0", "binary")
	got, err := update.BundleVersion(bundle)
	if err != nil {
		t.Fatalf("BundleVersion: %v", err)
	}
	if got != "0.3.0" {
		t.Fatalf("version = %q, want 0.3.0", got)
	}
}

func TestBundleVersionMissing(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "monctl.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bundle, "Contents", "Info.plist"), "<plist><dict></dict></plist>")
	if _, err := update.BundleVersion(bundle); err == nil {
		t.Fatal("BundleVersion = nil error, want missing-version failure")
	}
}

func TestLocateBundle(t *testing.T) {
	dir := t.TempDir()
	want, exe := makeBundle(t, dir, "0.2.0", "binary")
	// LocateBundle resolves /var -> /private/var on macOS.
	want, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := update.LocateBundle(exe)
	if err != nil {
		t.Fatalf("LocateBundle: %v", err)
	}
	if got != want {
		t.Fatalf("bundle = %s, want %s", got, want)
	}

	if runtime.GOOS != "windows" {
		// Creating symlinks on Windows needs extra privileges.
		link := filepath.Join(dir, "launcher")
		if err := os.Symlink(exe, link); err != nil {
			t.Fatal(err)
		}
		got, err = update.LocateBundle(link)
		if err != nil {
			t.Fatalf("LocateBundle(symlink): %v", err)
		}
		if got != want {
			t.Fatalf("symlink bundle = %s, want %s", got, want)
		}
	}
}

func TestLocateBundleRejectsBareBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "monctl")
	writeFile(t, exe, "binary")
	if _, err := update.LocateBundle(exe); !errors.Is(err, update.ErrNotAppBundle) {
		t.Fatalf("err = %v, want ErrNotAppBundle", err)
	}
}

func TestCheckInstallable(t *testing.T) {
	dir := t.TempDir()
	bundle, _ := makeBundle(t, dir, "0.2.0", "binary")
	if err := update.CheckInstallable(bundle); err != nil {
		t.Fatalf("CheckInstallable: %v", err)
	}
}

func TestCheckInstallableRejectsReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permission semantics as a non-root user")
	}
	parent := t.TempDir()
	bundle, _ := makeBundle(t, parent, "0.2.0", "binary")
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	err := update.CheckInstallable(bundle)
	if !errors.Is(err, update.ErrBundleNotWritable) {
		t.Fatalf("err = %v, want ErrBundleNotWritable", err)
	}
}

func TestBundleInstallerReplacesBundle(t *testing.T) {
	dir := t.TempDir()
	live, _ := makeBundle(t, dir, "0.2.0", "old app")
	zipPath := filepath.Join(dir, "update.zip")
	writeTestZip(t, zipPath, []zipEntry{
		{name: "monctl.app/Contents/Info.plist", body: plist("0.3.0")},
		{name: "monctl.app/Contents/MacOS/monctl", body: "new app", mode: 0o755},
	})
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	inst := &update.BundleInstaller{Bundle: live, ExpectedVersion: "v0.3.0"}
	staged, err := inst.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if filepath.Dir(staged) != dir {
		t.Fatalf("staged in %s, want the bundle's directory", filepath.Dir(staged))
	}
	if err := os.WriteFile(staged, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := inst.Install(staged); err != nil {
		t.Fatalf("Install: %v", err)
	}

	assertContent(t, filepath.Join(live, "Contents", "MacOS", "monctl"), "new app")
	if v, err := update.BundleVersion(live); err != nil || v != "0.3.0" {
		t.Fatalf("live version = %q, %v", v, err)
	}
	old := live + ".old"
	assertContent(t, filepath.Join(old, "Contents", "MacOS", "monctl"), "old app")
	info, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(info.ModTime()); age > time.Minute {
		t.Fatalf(".old mtime is %v old, want it refreshed at install time", age)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staged zip still exists after install")
	}
	assertNoUpdateLeftovers(t, dir)
}

func TestBundleInstallerRejectsVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	live, _ := makeBundle(t, dir, "0.2.0", "old app")
	zipPath := filepath.Join(dir, "update.zip")
	writeTestZip(t, zipPath, []zipEntry{
		{name: "monctl.app/Contents/Info.plist", body: plist("0.2.9")},
		{name: "monctl.app/Contents/MacOS/monctl", body: "wrong app"},
	})
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	inst := &update.BundleInstaller{Bundle: live, ExpectedVersion: "v0.3.0"}
	staged, err := inst.Stage()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := inst.Install(staged); err == nil {
		t.Fatal("Install = nil error, want version mismatch")
	}
	assertContent(t, filepath.Join(live, "Contents", "MacOS", "monctl"), "old app")
	if _, err := os.Stat(live + ".old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".old exists after rejected install")
	}
	assertNoUpdateLeftovers(t, dir)
}

// assertNoUpdateLeftovers fails if staging or extraction directories remain.
func assertNoUpdateLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "monctl.app" || name == "monctl.app.old" || name == "update.zip" {
			continue
		}
		t.Fatalf("leftover %q in %s", name, dir)
	}
}
