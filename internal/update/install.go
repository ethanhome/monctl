package update

import (
	"fmt"
	"os"
	"time"
)

// Installer stages a verified artifact next to the live one and swaps it
// into place, restoring the original if the swap fails.
type Installer interface {
	// Stage returns a path where the new artifact can be written. It must
	// be on the same volume as the live artifact so the final rename is
	// atomic.
	Stage() (path string, err error)
	// Install moves the staged artifact into place. On failure the live
	// artifact is left exactly as it was.
	Install(staged string) error
	// Abort discards a staged artifact after a failed download, verify or
	// install.
	Abort(staged string)
}

// BinaryInstaller replaces one executable file. It is used by the CLI and,
// later, by the Windows GUI.
type BinaryInstaller struct {
	// Target is the live executable path (symlinks resolved).
	Target string
}

// Stage creates an empty file named <target>.new beside the target. A crash
// during download can leave one behind; CleanupStaleOld removes it once it is
// a day old.
func (b *BinaryInstaller) Stage() (string, error) {
	path := b.Target + ".new"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("stage update: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("stage update: %w", err)
	}
	return path, nil
}

// Install renames the live file to <target>.old and the staged file into
// its place. A failure after the first rename restores the original.
func (b *BinaryInstaller) Install(staged string) error {
	// Match the live binary's permissions rather than assuming 0755: a
	// user who installed it as 0700 should keep it that way.
	mode := os.FileMode(0o755)
	if info, err := os.Stat(b.Target); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(staged, mode); err != nil {
		return fmt.Errorf("set staged mode: %w", err)
	}
	return swapPath(b.Target, staged, func(old string) error {
		// Rename preserves the old binary's mtime, which would make a version
		// installed weeks ago look stale immediately. The rollback copy's age
		// must start at the moment of the update.
		return stampRollback(old, time.Now())
	})
}

// stampRollback records when the rollback copy was made. On Windows the old
// copy of a running exe cannot be opened for a timestamp write (the loader
// only shares read/delete, not write), so fall back to a sidecar file whose
// mtime CleanupStaleOld prefers.
func stampRollback(old string, now time.Time) error {
	err := os.Chtimes(old, now, now)
	if err == nil {
		_ = os.Remove(old + ".stamp")
		return nil
	}
	if werr := os.WriteFile(old+".stamp", nil, 0o644); werr != nil {
		return fmt.Errorf("stamp rollback copy: %v (fallback: %v)", err, werr)
	}
	return nil
}

// swapPath moves target aside to target.old, runs stamp on the rollback copy,
// then moves staged into target. If any step fails, the original target is
// restored. Shared by the CLI binary installer and the macOS bundle installer.
func swapPath(target, staged string, stamp func(old string) error) error {
	old := target + ".old"
	// A previous update within 24h may still own the rollback name; this
	// update supersedes it. RemoveAll also handles a bundle (directory).
	_ = os.Remove(old + ".stamp")
	if err := os.RemoveAll(old); err != nil {
		return fmt.Errorf("remove previous rollback copy: %w", err)
	}
	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("move current version aside: %w", err)
	}
	if err := stamp(old); err != nil {
		_ = os.Rename(old, target)
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if rerr := os.Rename(old, target); rerr != nil {
			return fmt.Errorf("install new version: %w (restore failed: %v)", err, rerr)
		}
		return fmt.Errorf("install new version: %w", err)
	}
	return nil
}

// Abort removes the staged file; errors are ignored because the caller is
// already on a failure path.
func (b *BinaryInstaller) Abort(staged string) { _ = os.Remove(staged) }

// CleanupStaleOld removes <target>.old once it is older than maxAge. It is
// best-effort: a missing file, a permission error, or a still-running old
// binary simply leaves it for the next start. RemoveAll is required because
// a macOS rollback copy is a directory. A download interrupted by a crash
// also leaves <target>.new, which is removed after the same delay.
func CleanupStaleOld(target string, maxAge time.Duration) {
	old := target + ".old"
	stamp := old + ".stamp"
	info, err := os.Stat(old)
	if err != nil {
		_ = os.Remove(stamp)
	} else {
		age := info.ModTime()
		// Windows cannot stamp a running exe; swapPath writes a sidecar then,
		// and that sidecar is the authoritative update time.
		if si, err := os.Stat(stamp); err == nil {
			age = si.ModTime()
		}
		if time.Since(age) > maxAge {
			_ = os.RemoveAll(old)
			_ = os.Remove(stamp)
		}
	}
	if ni, err := os.Stat(target + ".new"); err == nil && time.Since(ni.ModTime()) > maxAge {
		_ = os.Remove(target + ".new")
	}
}
