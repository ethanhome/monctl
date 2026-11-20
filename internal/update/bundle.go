package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrReadOnlyLocation means the running bundle cannot be modified at all:
	// it was launched from a mounted disk image or an AppTranslocation path.
	// The user must copy it into Applications first.
	ErrReadOnlyLocation = errors.New("monctl is running from a read-only location")
	// ErrBundleNotWritable means the bundle's directory exists but the user
	// may not write to it (for example /Applications owned by another admin).
	ErrBundleNotWritable = errors.New("no permission to modify the app bundle")
	// ErrNotAppBundle means the executable is not inside a macOS .app.
	ErrNotAppBundle = errors.New("executable is not inside an app bundle")
)

// LocateBundle walks up from exe to the enclosing .app directory. It resolves
// symlinks first so the bundle that is actually running is replaced, not a
// launcher that points at it.
func LocateBundle(exe string) (string, error) {
	p, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("locate app bundle: %w", err)
	}
	for dir := filepath.Dir(p); ; {
		if strings.HasSuffix(dir, ".app") {
			plist := filepath.Join(dir, "Contents", "Info.plist")
			if info, err := os.Stat(plist); err == nil && !info.IsDir() {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("%w: %s", ErrNotAppBundle, exe)
}

// CheckInstallable reports whether bundle can be replaced in place, mapping
// the failure to one of the two user-facing downgrade reasons.
func CheckInstallable(bundle string) error {
	if isTranslocated(bundle) {
		return ErrReadOnlyLocation
	}
	f, err := os.CreateTemp(filepath.Dir(bundle), ".monctl-write-test-*")
	if err != nil {
		if errors.Is(err, syscall.EROFS) {
			return fmt.Errorf("%w: %v", ErrReadOnlyLocation, err)
		}
		return fmt.Errorf("%w: %v", ErrBundleNotWritable, err)
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return nil
}

func isTranslocated(path string) bool {
	return strings.Contains(path, "/AppTranslocation/")
}

// BundleInstaller replaces a whole macOS app bundle: the staged zip is
// unpacked beside it, its Info.plist version is checked against the release
// tag, then the live bundle is renamed aside and the new one moved in.
type BundleInstaller struct {
	// Bundle is the live .app path, normally from LocateBundle.
	Bundle string
	// ExpectedVersion is the release tag (with or without "v"). The bundle's
	// CFBundleShortVersionString must match it exactly.
	ExpectedVersion string
}

// Stage creates an empty zip beside the bundle. Same directory means same
// volume, so the final rename is atomic.
func (b *BundleInstaller) Stage() (string, error) {
	if err := CheckInstallable(b.Bundle); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(b.Bundle), ".monctl-update-*.zip")
	if err != nil {
		return "", fmt.Errorf("stage update: %w", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("stage update: %w", err)
	}
	return name, nil
}

// Install unpacks and verifies the staged zip, then swaps it in. Any failure
// before the final rename leaves the live bundle untouched.
func (b *BundleInstaller) Install(staged string) error {
	defer os.Remove(staged)

	parent := filepath.Dir(b.Bundle)
	work, err := os.MkdirTemp(parent, ".monctl-update-*")
	if err != nil {
		return fmt.Errorf("prepare update: %w", err)
	}
	defer os.RemoveAll(work)

	newBundle, err := ExtractApp(staged, work)
	if err != nil {
		return err
	}
	got, err := BundleVersion(newBundle)
	if err != nil {
		return err
	}
	want := strings.TrimPrefix(b.ExpectedVersion, "v")
	if got != want {
		return fmt.Errorf("bundle version %q does not match release %q", got, b.ExpectedVersion)
	}

	return swapPath(b.Bundle, newBundle, func(old string) error {
		// Rename keeps the old bundle's mtime; without this the rollback copy
		// would look stale and be deleted at the next launch.
		return stampRollback(old, time.Now())
	})
}

// Abort discards a staged zip after a failed download, verify or install.
func (b *BundleInstaller) Abort(staged string) { _ = os.Remove(staged) }
