package update

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// netDialer bounds connection setup; there is no overall download timeout at
// the client level.
var netDialer = &net.Dialer{Timeout: 10 * time.Second}

// Fetcher is the external boundary for downloading bytes (release assets
// and SHA256SUMS). Tests supply an in-memory implementation.
type Fetcher interface {
	Get(ctx context.Context, url string) (io.ReadCloser, error)
}

// HTTPFetcher downloads over HTTP. The zero value is ready to use: it has no
// overall timeout (release assets can be large) but bounds connection and
// response-header waits, and honours the standard proxy environment.
type HTTPFetcher struct {
	Client *http.Client
}

// Get implements Fetcher.
func (f *HTTPFetcher) Get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "monctl")
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

func (f *HTTPFetcher) httpClient() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}
	clone := tr.Clone()
	clone.ResponseHeaderTimeout = 10 * time.Second
	clone.DialContext = netDialer.DialContext
	return &http.Client{Transport: clone}
}
