package scanner

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type RenderedLinkExtractor interface {
	Links(ctx context.Context, pageURL string, root *url.URL) ([]string, error)
}

type ChromeRenderedLinkExtractor struct {
	Chrome  string
	Timeout time.Duration
}

func (r ChromeRenderedLinkExtractor) Links(ctx context.Context, pageURL string, root *url.URL) ([]string, error) {
	if r.Timeout <= 0 {
		r.Timeout = 15 * time.Second
	}
	chrome, err := (ChromeVisualRunner{Chrome: r.Chrome}).chromePath()
	if err != nil {
		return nil, err
	}
	renderCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	// Dedicated throwaway profile so headless does not hand off to an already
	// running Chrome (which would return the wrong DOM or hang on the lock).
	profile, err := os.MkdirTemp("", "eds-dom-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(profile)
	cmd := exec.CommandContext(renderCtx, chrome,
		"--headless=new",
		"--disable-gpu",
		"--hide-scrollbars",
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-dev-shm-usage",
		"--user-data-dir="+profile,
		"--virtual-time-budget=5000",
		"--dump-dom",
		pageURL,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if renderCtx.Err() != nil {
			return nil, renderCtx.Err()
		}
		return nil, fmt.Errorf("chrome rendered discovery failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	page, err := AnalyzeHTML(pageURL, bytes.NewReader(output), root)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(page.Links))
	for _, link := range page.Links {
		if link.Kind == "internal" {
			urls = append(urls, link.URL)
		}
	}
	return urls, nil
}
