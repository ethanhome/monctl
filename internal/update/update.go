// Package update checks for newer monctl releases.
package update

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/mod/semver"
)

// DevVersion is the version reported by local builds.
const DevVersion = "dev"

// ErrNotReleaseBuild is returned when the running binary has no release
// version: "dev" or a value that is not a v-prefixed semver tag. Such builds
// never check for or install updates.
var ErrNotReleaseBuild = errors.New("not a release build: update check disabled")

// ErrInvalidVersion is returned when a release tag is not valid semver.
// Silently treating it as "up to date" would be unsafe.
var ErrInvalidVersion = errors.New("invalid version")

// Release is one published version of monctl.
type Release struct {
	Tag    string
	Assets []Asset
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string
	URL  string
}

// Platform is the target an update asset must match.
type Platform struct {
	OS   string
	Arch string
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// Source is the external boundary: where the latest release comes from.
type Source interface {
	Latest(ctx context.Context) (Release, error)
}

// Result describes the outcome of a version check.
type Result struct {
	// Available is true when the latest release is strictly newer.
	Available bool
	Current   string
	Latest    string
	// Asset is the download that applies to this platform, when Available.
	Asset Asset
	// Release is the release the check compared against, including its
	// SHA256SUMS asset, so Apply does not need a second lookup.
	Release Release
}

// Check compares the running version with the latest release from src and
// selects the CLI asset for platform.
func Check(ctx context.Context, src Source, current string, platform Platform) (Result, error) {
	return check(ctx, src, current, platform, cliAssetName)
}

// CheckGUI is Check for the Wails GUI, selecting the GUI artifact instead of
// the CLI binary: the macOS app bundle zip on darwin, the GUI .exe on Windows.
func CheckGUI(ctx context.Context, src Source, current string, platform Platform) (Result, error) {
	return check(ctx, src, current, platform, guiAssetName)
}

type assetNamer func(Platform) (string, error)

func check(ctx context.Context, src Source, current string, platform Platform, assetName assetNamer) (Result, error) {
	if current == DevVersion || !semver.IsValid(current) {
		return Result{}, fmt.Errorf("%w (version %q)", ErrNotReleaseBuild, current)
	}
	rel, err := src.Latest(ctx)
	if err != nil {
		return Result{}, err
	}
	if !semver.IsValid(rel.Tag) {
		return Result{}, fmt.Errorf("%w: release tag %q", ErrInvalidVersion, rel.Tag)
	}
	res := Result{Current: current, Latest: rel.Tag, Release: rel}
	if semver.Compare(rel.Tag, current) <= 0 {
		return res, nil
	}
	name, err := assetName(platform)
	if err != nil {
		return Result{}, err
	}
	asset, ok := findAsset(rel.Assets, name)
	if !ok {
		return Result{}, fmt.Errorf("release %s has no asset %q", rel.Tag, name)
	}
	res.Available = true
	res.Asset = asset
	return res, nil
}

// cliAssetName maps a platform to the CLI artifact published by release.yml.
func cliAssetName(p Platform) (string, error) {
	switch p.OS + "/" + p.Arch {
	case "windows/amd64":
		return "monctl-windows-amd64.exe", nil
	case "darwin/arm64":
		return "monctl-darwin-arm64", nil
	default:
		return "", fmt.Errorf("unsupported platform %s", p)
	}
}

// guiAssetName maps a platform to the artifact the GUI installs itself from.
// macOS replaces the whole app bundle, so it needs the zip rather than a bare
// binary; Windows reuses the GUI .exe.
func guiAssetName(p Platform) (string, error) {
	switch p.OS + "/" + p.Arch {
	case "darwin/arm64":
		return "monctl-macos-arm64.zip", nil
	case "windows/amd64":
		return "monctl-gui-windows-amd64.exe", nil
	default:
		return "", fmt.Errorf("unsupported platform %s", p)
	}
}

func findAsset(assets []Asset, name string) (Asset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}
