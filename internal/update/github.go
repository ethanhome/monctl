package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	apiBase     = "https://api.github.com"
	defaultRepo = "ethanhome/monctl"
)

// GitHubSource reads the latest stable release from GitHub's REST API.
// releases/latest skips prereleases and drafts, which is exactly the stable
// channel this tool supports.
//
// The zero value is ready to use: it queries the monctl repository over
// api.github.com with a 10-second timeout. Tests override the fields to
// point at httptest.
type GitHubSource struct {
	Client  *http.Client
	Repo    string
	BaseURL string
}

// Latest implements Source.
func (g *GitHubSource) Latest(ctx context.Context) (Release, error) {
	base := g.BaseURL
	if base == "" {
		base = apiBase
	}
	repo := g.Repo
	if repo == "" {
		repo = defaultRepo
	}
	url := strings.TrimSuffix(base, "/") + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", "monctl")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := g.httpClient().Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("github: latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github: latest release: %s", resp.Status)
	}

	var doc struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return Release{}, fmt.Errorf("github: decode latest release: %w", err)
	}
	rel := Release{Tag: doc.Tag, Assets: make([]Asset, 0, len(doc.Assets))}
	for _, a := range doc.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL})
	}
	return rel, nil
}

func (g *GitHubSource) httpClient() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}
