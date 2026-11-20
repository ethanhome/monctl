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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func TestBinaryInstallerReplacesAndKeepsOld(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	writeFile(t, target, "old binary")

	inst := &update.BinaryInstaller{Target: target}
	staged, err := inst.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if staged != target+".new" {
		t.Fatalf("staged = %s, want %s", staged, target+".new")
	}
	if filepath.Dir(staged) != dir {
		t.Fatalf("staged in %s, want the target directory %s", filepath.Dir(staged), dir)
	}
	writeFile(t, staged, "new binary")

	if err := inst.Install(staged); err != nil {
		t.Fatalf("Install: %v", err)
	}
	assertContent(t, target, "new binary")
	assertContent(t, target+".old", "old binary")
}

func TestBinaryInstallerRestoresTargetWhenInstallFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	writeFile(t, target, "old binary")

	inst := &update.BinaryInstaller{Target: target}
	staged, err := inst.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// Removing the staged file makes the second rename fail after the live
	// binary has already been moved aside.
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}

	if err := inst.Install(staged); err == nil {
		t.Fatal("Install = nil error, want failure")
	}
	assertContent(t, target, "old binary")
	if _, err := os.Stat(target + ".old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".old still exists after rollback: %v", err)
	}
}

func TestCleanupStaleOld(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	old := target + ".old"
	writeFile(t, old, "previous binary")
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}

	update.CleanupStaleOld(target, 24*time.Hour)

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".old still exists after cleanup: %v", err)
	}
}

func TestCleanupKeepsFreshOld(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	old := target + ".old"
	writeFile(t, old, "previous binary")

	update.CleanupStaleOld(target, 24*time.Hour)

	if _, err := os.Stat(old); err != nil {
		t.Fatalf("fresh .old was removed: %v", err)
	}
}

// A macOS rollback copy is a directory, so cleanup must recurse.
func TestCleanupStaleOldDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl.app")
	old := target + ".old"
	if err := os.MkdirAll(filepath.Join(old, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(old, "Contents", "MacOS", "monctl"), "old app")
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}

	update.CleanupStaleOld(target, 24*time.Hour)

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale .old directory still exists: %v", err)
	}
}

// Windows cannot set timestamps on the running exe, so the rollback copy may
// carry a sidecar stamp. A fresh stamp keeps an old-mtime .old alive.
func TestCleanupPrefersFreshStamp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl.exe")
	old := target + ".old"
	writeFile(t, old, "previous binary")
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	writeFile(t, old+".stamp", "")

	update.CleanupStaleOld(target, 24*time.Hour)

	if _, err := os.Stat(old); err != nil {
		t.Fatalf("fresh-stamped .old was removed: %v", err)
	}
}

func TestCleanupRemovesStaleStamp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl.exe")
	old := target + ".old"
	writeFile(t, old, "previous binary")
	stamp := old + ".stamp"
	writeFile(t, stamp, "")
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(stamp, stale, stale); err != nil {
		t.Fatal(err)
	}

	update.CleanupStaleOld(target, 24*time.Hour)

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".old still exists after stale stamp: %v", err)
	}
	if _, err := os.Stat(stamp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".stamp still exists after cleanup: %v", err)
	}
}

func TestCleanupRemovesCrashedDownload(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	newFile := target + ".new"
	writeFile(t, newFile, "partial download")
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(newFile, stale, stale); err != nil {
		t.Fatal(err)
	}

	update.CleanupStaleOld(target, 24*time.Hour)

	if _, err := os.Stat(newFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale .new still exists: %v", err)
	}
}

func TestBinaryInstallerRefreshesOldTimestamp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	writeFile(t, target, "old binary")
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(target, past, past); err != nil {
		t.Fatal(err)
	}

	inst := &update.BinaryInstaller{Target: target}
	staged, err := inst.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	writeFile(t, staged, "new binary")
	if err := inst.Install(staged); err != nil {
		t.Fatalf("Install: %v", err)
	}

	info, err := os.Stat(target + ".old")
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(info.ModTime()); age > time.Minute {
		t.Fatalf(".old mtime is %v old, want it refreshed at install time", age)
	}
}

func TestBinaryInstallerPreservesTargetMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	writeFile(t, target, "old binary")
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}

	inst := &update.BinaryInstaller{Target: target}
	staged, err := inst.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	writeFile(t, staged, "new binary")
	if err := inst.Install(staged); err != nil {
		t.Fatalf("Install: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode = %o, want 700", got)
	}
}

func TestBinaryInstallerStageFailsInReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are not enforced the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	writeFile(t, target, "old binary")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	inst := &update.BinaryInstaller{Target: target}
	if _, err := inst.Stage(); err == nil {
		t.Fatal("Stage = nil error in a read-only directory, want failure")
	}
}
