package update_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bacnh85/monctl/internal/update"
)

var (
	darwinArm  = update.Platform{OS: "darwin", Arch: "arm64"}
	windowsAmd = update.Platform{OS: "windows", Arch: "amd64"}
)

// fakeSource stands in for GitHub: tests never touch the network.
type fakeSource struct {
	rel update.Release
	err error
}

func (f fakeSource) Latest(context.Context) (update.Release, error) {
	return f.rel, f.err
}

func releaseWithCLIAssets() update.Release {
	return update.Release{
		Tag: "v0.3.0",
		Assets: []update.Asset{
			{Name: "monctl-darwin-arm64", URL: "https://example.test/monctl-darwin-arm64"},
			{Name: "monctl-windows-amd64.exe", URL: "https://example.test/monctl-windows-amd64.exe"},
		},
	}
}

func TestCheckReportsNewerRelease(t *testing.T) {
	got, err := update.Check(context.Background(), fakeSource{rel: releaseWithCLIAssets()}, "v0.2.0", darwinArm)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !got.Available {
		t.Fatal("Available = false, want true")
	}
	if got.Current != "v0.2.0" || got.Latest != "v0.3.0" {
		t.Fatalf("versions = %q -> %q, want v0.2.0 -> v0.3.0", got.Current, got.Latest)
	}
	if got.Asset.Name != "monctl-darwin-arm64" {
		t.Fatalf("asset = %q, want monctl-darwin-arm64", got.Asset.Name)
	}
	if got.Asset.URL != "https://example.test/monctl-darwin-arm64" {
		t.Fatalf("asset URL = %q", got.Asset.URL)
	}
}

func TestCheckSelectsWindowsAsset(t *testing.T) {
	got, err := update.Check(context.Background(), fakeSource{rel: releaseWithCLIAssets()}, "v0.2.0", windowsAmd)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Asset.Name != "monctl-windows-amd64.exe" {
		t.Fatalf("asset = %q, want monctl-windows-amd64.exe", got.Asset.Name)
	}
}

func TestCheckReportsNoUpdateWhenNotNewer(t *testing.T) {
	src := fakeSource{rel: update.Release{Tag: "v0.3.0"}}
	for _, current := range []string{"v0.3.0", "v0.4.0"} {
		got, err := update.Check(context.Background(), src, current, darwinArm)
		if err != nil {
			t.Fatalf("Check(%s): %v", current, err)
		}
		if got.Available {
			t.Fatalf("Check(%s): Available = true, want false", current)
		}
		if got.Asset.Name != "" {
			t.Fatalf("Check(%s): asset = %q, want empty", current, got.Asset.Name)
		}
	}
}

func TestCheckOrdersPrereleasesBeforeFinalReleases(t *testing.T) {
	ctx := context.Background()
	got, err := update.Check(ctx, fakeSource{rel: releaseWithCLIAssets()}, "v0.3.0-rc1", darwinArm)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !got.Available {
		t.Fatal("rc1 -> final: Available = false, want true")
	}
	got, err = update.Check(ctx, fakeSource{rel: update.Release{Tag: "v0.3.0-rc1"}}, "v0.3.0", darwinArm)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Available {
		t.Fatal("final -> rc1: Available = true, want false")
	}
}

func TestCheckRefusesNonReleaseBuild(t *testing.T) {
	src := fakeSource{err: errors.New("source must not be consulted")}
	for _, current := range []string{"dev", "0.2.0", ""} {
		_, err := update.Check(context.Background(), src, current, darwinArm)
		if !errors.Is(err, update.ErrNotReleaseBuild) {
			t.Fatalf("Check(%q): err = %v, want ErrNotReleaseBuild", current, err)
		}
	}
}

func TestCheckRejectsInvalidReleaseTag(t *testing.T) {
	_, err := update.Check(context.Background(), fakeSource{rel: update.Release{Tag: "latest"}}, "v0.2.0", darwinArm)
	if !errors.Is(err, update.ErrInvalidVersion) {
		t.Fatalf("err = %v, want ErrInvalidVersion", err)
	}
}

func TestCheckFailsWhenReleaseLacksThePlatformAsset(t *testing.T) {
	src := fakeSource{rel: update.Release{Tag: "v0.3.0"}}
	if _, err := update.Check(context.Background(), src, "v0.2.0", darwinArm); err == nil {
		t.Fatal("err = nil, want missing-asset error")
	}
}

func TestCheckFailsOnUnsupportedPlatform(t *testing.T) {
	src := fakeSource{rel: update.Release{Tag: "v0.3.0", Assets: []update.Asset{{Name: "monctl-darwin-arm64"}}}}
	if _, err := update.Check(context.Background(), src, "v0.2.0", update.Platform{OS: "linux", Arch: "amd64"}); err == nil {
		t.Fatal("err = nil, want unsupported-platform error")
	}
}

func releaseWithGUIAssets() update.Release {
	return update.Release{
		Tag: "v0.3.0",
		Assets: []update.Asset{
			{Name: "monctl-darwin-arm64", URL: "https://example.test/monctl-darwin-arm64"},
			{Name: "monctl-gui-windows-amd64.exe", URL: "https://example.test/monctl-gui-windows-amd64.exe"},
			{Name: "monctl-macos-arm64.zip", URL: "https://example.test/monctl-macos-arm64.zip"},
		},
	}
}

func TestCheckGUISelectsAppBundleZipOnMac(t *testing.T) {
	got, err := update.CheckGUI(context.Background(), fakeSource{rel: releaseWithGUIAssets()}, "v0.2.0", darwinArm)
	if err != nil {
		t.Fatalf("CheckGUI: %v", err)
	}
	if got.Asset.Name != "monctl-macos-arm64.zip" {
		t.Fatalf("asset = %q, want monctl-macos-arm64.zip", got.Asset.Name)
	}
}

func TestCheckGUISelectsGUIExeOnWindows(t *testing.T) {
	got, err := update.CheckGUI(context.Background(), fakeSource{rel: releaseWithGUIAssets()}, "v0.2.0", windowsAmd)
	if err != nil {
		t.Fatalf("CheckGUI: %v", err)
	}
	if got.Asset.Name != "monctl-gui-windows-amd64.exe" {
		t.Fatalf("asset = %q, want monctl-gui-windows-amd64.exe", got.Asset.Name)
	}
}
