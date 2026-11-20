package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	sumsAssetName = "SHA256SUMS"
	maxSumsBytes  = 1 << 20
)

// Upgrader checks for a newer release and applies it: fetch the checksums,
// download the platform asset, verify its SHA256, then hand it to the
// Installer. Check and Apply are separate so a caller can show the plan and
// ask for confirmation between them.
type Upgrader struct {
	Source    Source
	Fetcher   Fetcher
	Installer Installer
	Current   string
	Platform  Platform
	// OnStep, when set, reports coarse progress before each slow phase:
	// StageDownload then StageInstall.
	OnStep func(step string)
}

// Progress steps passed to Upgrader.OnStep.
const (
	StageDownload = "download"
	StageInstall  = "install"
)

// Check reports whether a newer release is available.
func (u *Upgrader) Check(ctx context.Context) (Result, error) {
	return Check(ctx, u.Source, u.Current, u.Platform)
}

// Apply installs the release described by a Result from Check. It is a no-op
// when no update is available. Any failure leaves the current installation
// untouched.
func (u *Upgrader) Apply(ctx context.Context, res Result) error {
	if !res.Available {
		return nil
	}
	if u.Fetcher == nil || u.Installer == nil {
		return errors.New("update: Fetcher and Installer are required")
	}
	sumsAsset, ok := findAsset(res.Release.Assets, sumsAssetName)
	if !ok {
		return fmt.Errorf("release %s has no %s asset", res.Latest, sumsAssetName)
	}
	sums, err := u.fetchChecksums(ctx, sumsAsset.URL)
	if err != nil {
		return err
	}
	want, ok := sums[res.Asset.Name]
	if !ok {
		return fmt.Errorf("%s has no entry for %q", sumsAssetName, res.Asset.Name)
	}

	staged, err := u.Installer.Stage()
	if err != nil {
		return fmt.Errorf("stage update: %w", err)
	}
	u.reportStep(StageDownload)
	if err := u.download(ctx, res.Asset.URL, staged, want); err != nil {
		u.Installer.Abort(staged)
		return fmt.Errorf("%s: %w", res.Asset.Name, err)
	}
	u.reportStep(StageInstall)
	if err := u.Installer.Install(staged); err != nil {
		u.Installer.Abort(staged)
		return fmt.Errorf("install update: %w", err)
	}
	return nil
}

func (u *Upgrader) reportStep(step string) {
	if u.OnStep != nil {
		u.OnStep(step)
	}
}

func (u *Upgrader) fetchChecksums(ctx context.Context, url string) (map[string]string, error) {
	rc, err := u.Fetcher.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxSumsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sumsAssetName, err)
	}
	if len(data) > maxSumsBytes {
		return nil, fmt.Errorf("%s: file too large", sumsAssetName)
	}
	sums, err := ParseChecksums(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return sums, nil
}

// download streams url into path while hashing, so a large asset is never
// held in memory and a corrupted one never reaches the installer.
func (u *Upgrader) download(ctx context.Context, url, path, want string) error {
	rc, err := u.Fetcher.Get(ctx, url)
	if err != nil {
		return err
	}
	defer rc.Close()

	// The staged file already exists (Stage created it); the mode is applied
	// by the installer when it inherits the live binary's permissions.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("open staged file: %w", err)
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), rc)
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("download: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("write staged file: %w", closeErr)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}
