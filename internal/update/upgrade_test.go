package update_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bacnh85/monctl/internal/update"
)

type mapFetcher map[string]string

func (f mapFetcher) Get(_ context.Context, url string) (io.ReadCloser, error) {
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

type failingInstaller struct {
	installed bool
	aborted   bool
}

func (f *failingInstaller) Stage() (string, error) {
	file, err := os.CreateTemp("", "monctl-test-staged-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}
func (f *failingInstaller) Install(string) error { return errors.New("install exploded") }
func (f *failingInstaller) Abort(staged string) {
	f.aborted = true
	os.Remove(staged)
}

const (
	assetURL = "https://example.test/monctl-darwin-arm64"
	sumsURL  = "https://example.test/SHA256SUMS"
)

func upgradableResult(assets ...update.Asset) update.Result {
	rel := update.Release{Tag: "v0.3.0", Assets: assets}
	return update.Result{
		Available: true,
		Current:   "v0.2.0",
		Latest:    "v0.3.0",
		Asset:     assets[0],
		Release:   rel,
	}
}

func cliAssets() []update.Asset {
	return []update.Asset{
		{Name: "monctl-darwin-arm64", URL: assetURL},
		{Name: "SHA256SUMS", URL: sumsURL},
	}
}

func TestUpgraderApplyInstallsVerifiedBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	writeFile(t, target, "old binary")

	up := &update.Upgrader{
		Fetcher: mapFetcher{
			assetURL: "new binary",
			sumsURL:  sha256Hex("new binary") + "  monctl-darwin-arm64\n",
		},
		Installer: &update.BinaryInstaller{Target: target},
	}
	if err := up.Apply(context.Background(), upgradableResult(cliAssets()...)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertContent(t, target, "new binary")
	assertContent(t, target+".old", "old binary")
}

func TestUpgraderApplyRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "monctl")
	writeFile(t, target, "old binary")

	up := &update.Upgrader{
		Fetcher: mapFetcher{
			assetURL: "tampered binary",
			sumsURL:  sha256Hex("new binary") + "  monctl-darwin-arm64\n",
		},
		Installer: &update.BinaryInstaller{Target: target},
	}
	if err := up.Apply(context.Background(), upgradableResult(cliAssets()...)); err == nil {
		t.Fatal("Apply = nil error, want checksum mismatch")
	}
	assertContent(t, target, "old binary")
	if _, err := os.Stat(target + ".old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".old exists after failed verify: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "monctl" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory after failed verify = %v, want only monctl", names)
	}
}

func TestUpgraderApplyRequiresChecksumEntry(t *testing.T) {
	target := filepath.Join(t.TempDir(), "monctl")
	writeFile(t, target, "old binary")

	cases := map[string][]update.Asset{
		"no SHA256SUMS asset": {
			{Name: "monctl-darwin-arm64", URL: assetURL},
		},
		"missing entry": cliAssets(),
	}
	for name, assets := range cases {
		t.Run(name, func(t *testing.T) {
			fetcher := mapFetcher{assetURL: "new binary"}
			if name == "missing entry" {
				fetcher[sumsURL] = sha256Hex("x") + "  some-other-file\n"
			}
			up := &update.Upgrader{
				Fetcher:   fetcher,
				Installer: &update.BinaryInstaller{Target: target},
			}
			if err := up.Apply(context.Background(), upgradableResult(assets...)); err == nil {
				t.Fatal("Apply = nil error, want missing-checksum error")
			}
			assertContent(t, target, "old binary")
		})
	}
}

func TestUpgraderApplyAbortsOnInstallFailure(t *testing.T) {
	inst := &failingInstaller{}
	up := &update.Upgrader{
		Fetcher: mapFetcher{
			assetURL: "new binary",
			sumsURL:  sha256Hex("new binary") + "  monctl-darwin-arm64\n",
		},
		Installer: inst,
	}
	if err := up.Apply(context.Background(), upgradableResult(cliAssets()...)); err == nil {
		t.Fatal("Apply = nil error, want install failure")
	}
	if !inst.aborted {
		t.Fatal("Abort was not called after a failed install")
	}
}

func TestUpgraderApplyIsNoOpWhenAlreadyLatest(t *testing.T) {
	up := &update.Upgrader{}
	res := update.Result{Available: false, Current: "v0.3.0", Latest: "v0.3.0"}
	if err := up.Apply(context.Background(), res); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestUpgraderApplyRejectsOversizedChecksums(t *testing.T) {
	target := filepath.Join(t.TempDir(), "monctl")
	writeFile(t, target, "old binary")

	up := &update.Upgrader{
		Fetcher: mapFetcher{
			assetURL: "new binary",
			sumsURL:  strings.Repeat("a", 1<<20+1),
		},
		Installer: &update.BinaryInstaller{Target: target},
	}
	err := up.Apply(context.Background(), upgradableResult(cliAssets()...))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want oversized-SHA256SUMS error", err)
	}
	assertContent(t, target, "old binary")
}

func TestUpgraderReportsDownloadAndInstallSteps(t *testing.T) {
	target := filepath.Join(t.TempDir(), "monctl")
	writeFile(t, target, "old binary")

	var steps []string
	up := &update.Upgrader{
		Fetcher: mapFetcher{
			assetURL: "new binary",
			sumsURL:  sha256Hex("new binary") + "  monctl-darwin-arm64\n",
		},
		Installer: &update.BinaryInstaller{Target: target},
		OnStep:    func(step string) { steps = append(steps, step) },
	}
	if err := up.Apply(context.Background(), upgradableResult(cliAssets()...)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := []string{update.StageDownload, update.StageInstall}; strings.Join(steps, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
}
