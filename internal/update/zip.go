package update

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AppBundleName is the only top-level entry a macOS update zip may contain.
const AppBundleName = "monctl.app"

// ExtractApp unpacks a macOS update zip into destDir and returns the path of
// the app bundle inside it. The zip must contain exactly one top-level entry,
// monctl.app. Absolute paths, ".." traversal and symlink entries are rejected
// before anything is written outside destDir.
func ExtractApp(zipPath, destDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open update zip: %w", err)
	}
	defer r.Close()

	if err := validateAppZip(r.File); err != nil {
		return "", err
	}
	for _, f := range r.File {
		if err := extractEntry(f, destDir); err != nil {
			return "", err
		}
	}
	bundle := filepath.Join(destDir, AppBundleName)
	info, err := os.Stat(bundle)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("update zip has no %s directory", AppBundleName)
	}
	return bundle, nil
}

func validateAppZip(files []*zip.File) error {
	if len(files) == 0 {
		return fmt.Errorf("update zip is empty")
	}
	sawBundle := false
	for _, f := range files {
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" || strings.HasPrefix(f.Name, "/") || filepath.IsAbs(name) {
			return fmt.Errorf("update zip has unsafe path %q", f.Name)
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return fmt.Errorf("update zip has unsafe path %q", f.Name)
			}
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("update zip has symlink entry %q", f.Name)
		}
		first := strings.SplitN(name, "/", 2)[0]
		if first != AppBundleName {
			return fmt.Errorf("update zip entry %q is outside %s", f.Name, AppBundleName)
		}
		sawBundle = true
	}
	if !sawBundle {
		return fmt.Errorf("update zip has no %s", AppBundleName)
	}
	return nil
}

func extractEntry(f *zip.File, destDir string) error {
	target := filepath.Join(destDir, filepath.FromSlash(f.Name))
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("update zip entry %q escapes the destination", f.Name)
	}

	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", f.Name, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", f.Name, err)
	}
	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("read %s: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", f.Name, err)
	}
	_, copyErr := io.Copy(out, rc)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("extract %s: %w", f.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("extract %s: %w", f.Name, closeErr)
	}
	// The umask can strip bits from the mode passed to OpenFile; the bundle's
	// executable must stay executable.
	if err := os.Chmod(target, mode); err != nil {
		return fmt.Errorf("set mode on %s: %w", f.Name, err)
	}
	return nil
}
