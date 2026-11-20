package update_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bacnh85/monctl/internal/update"
)

type zipEntry struct {
	name string
	body string
	mode os.FileMode
}

func writeTestZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractAppExtractsBundle(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "update.zip")
	writeTestZip(t, zipPath, []zipEntry{
		{name: "monctl.app/", mode: os.ModeDir | 0o755},
		{name: "monctl.app/Contents/Info.plist", body: "plist"},
		{name: "monctl.app/Contents/MacOS/monctl", body: "binary", mode: 0o755},
	})

	dest := filepath.Join(dir, "out")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle, err := update.ExtractApp(zipPath, dest)
	if err != nil {
		t.Fatalf("ExtractApp: %v", err)
	}
	if bundle != filepath.Join(dest, "monctl.app") {
		t.Fatalf("bundle = %s", bundle)
	}
	assertContent(t, filepath.Join(bundle, "Contents", "Info.plist"), "plist")
	exe := filepath.Join(bundle, "Contents", "MacOS", "monctl")
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX execute bit; the bundle is only ever unpacked on
	// macOS, so only assert this where the bit exists.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("executable lost its x bit: %v", info.Mode())
	}
}

func TestExtractAppRejectsUnsafeEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []zipEntry
	}{
		{
			name:    "parent traversal",
			entries: []zipEntry{{name: "monctl.app/../../evil", body: "x"}},
		},
		{
			name:    "absolute path",
			entries: []zipEntry{{name: "/monctl.app/evil", body: "x"}},
		},
		{
			name:    "symlink",
			entries: []zipEntry{{name: "monctl.app/Contents/link", body: "/etc/passwd", mode: os.ModeSymlink | 0o777}},
		},
		{
			name:    "second top-level entry",
			entries: []zipEntry{{name: "monctl.app/Contents/Info.plist"}, {name: "README", body: "x"}},
		},
		{
			name:    "wrong top-level directory",
			entries: []zipEntry{{name: "other.app/Contents/Info.plist"}},
		},
		{
			name:    "empty zip",
			entries: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			zipPath := filepath.Join(dir, "update.zip")
			writeTestZip(t, zipPath, tc.entries)
			dest := filepath.Join(dir, "out")
			if err := os.Mkdir(dest, 0o755); err != nil {
				t.Fatal(err)
			}

			if _, err := update.ExtractApp(zipPath, dest); err == nil {
				t.Fatal("ExtractApp = nil error, want rejection")
			}
			entries, err := os.ReadDir(dest)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("rejected zip wrote %d entries into the destination", len(entries))
			}
		})
	}
}
