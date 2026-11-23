package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	fetchTimeout = 10 * time.Second
	// maxSourceSize caps how much of a config file or URL is read, so a
	// misbehaving endpoint can't exhaust memory.
	maxSourceSize = 32 << 20
)

// IsURL reports whether a config source is an http(s) URL rather than a
// file path.
func IsURL(src string) bool {
	return strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
}

// readSource returns the raw content of src, which is either a local file
// path or an http(s) URL fetched with GET.
func readSource(ctx context.Context, src string) ([]byte, error) {
	if !IsURL(src) {
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readLimited(f)
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetching: unexpected status %s", resp.Status)
	}
	return readLimited(resp.Body)
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSourceSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceSize {
		return nil, fmt.Errorf("larger than %d bytes", maxSourceSize)
	}
	return data, nil
}

func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
