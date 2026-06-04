package scanner

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
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
	cmd := exec.CommandContext(renderCtx, chrome,
		"--headless=new",
		"--disable-gpu",
		"--hide-scrollbars",
		"--no-sandbox",
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
