package update_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bacnh85/monctl/internal/update"
)

func TestGitHubSourceParsesLatestRelease(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		io.WriteString(w, `{
			"tag_name": "v0.3.0",
			"assets": [
				{"name": "monctl-darwin-arm64", "browser_download_url": "https://example.test/a", "size": 42}
			]
		}`)
	}))
	defer srv.Close()

	src := &update.GitHubSource{Client: srv.Client(), Repo: "owner/repo", BaseURL: srv.URL}
	rel, err := src.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if gotPath != "/repos/owner/repo/releases/latest" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotUA == "" {
		t.Fatal("User-Agent header missing")
	}
	if rel.Tag != "v0.3.0" {
		t.Fatalf("tag = %q, want v0.3.0", rel.Tag)
	}
	if len(rel.Assets) != 1 {
		t.Fatalf("assets = %d, want 1", len(rel.Assets))
	}
	a := rel.Assets[0]
	if a.Name != "monctl-darwin-arm64" || a.URL != "https://example.test/a" {
		t.Fatalf("asset = %+v", a)
	}
}

func TestGitHubSourceReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	src := &update.GitHubSource{Client: srv.Client(), Repo: "owner/repo", BaseURL: srv.URL}
	if _, err := src.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want 404 error", err)
	}
}

func TestGitHubSourceHonoursClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, `{"tag_name": "v0.3.0"}`)
	}))
	defer srv.Close()

	src := &update.GitHubSource{
		Client:  &http.Client{Timeout: 20 * time.Millisecond},
		Repo:    "owner/repo",
		BaseURL: srv.URL,
	}
	if _, err := src.Latest(context.Background()); err == nil {
		t.Fatal("err = nil, want timeout error")
	}
}
