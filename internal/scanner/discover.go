package scanner

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DiscoveredURL struct {
	URL    string
	Source string
}

type Discoverer struct {
	Client *http.Client
}

func (d Discoverer) Discover(ctx context.Context, start *url.URL) ([]string, error) {
	seeds, _ := d.DiscoverDetailed(ctx, start)
	pages := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		pages = append(pages, seed.URL)
	}
	return pages, nil
}

func (d Discoverer) DiscoverDetailed(ctx context.Context, start *url.URL) ([]DiscoveredURL, DiscoveryReport) {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	seen := map[string]bool{}
	report := DiscoveryReport{RootURL: start.String(), Warnings: []string{}}
	var pages []DiscoveredURL
	add := func(raw string, source string) {
		normalized, ok := normalizePageURL(raw, start)
		if !ok {
			if looksLikeAssetOrDownload(raw, start) {
				report.SkippedAssets++
			}
			return
		}
		parsed, err := url.Parse(normalized)
		if err != nil {
			return
		}
		if !sameOrigin(parsed, start) {
			report.SkippedExternal++
			return
		}
		if seen[normalized] {
			report.Duplicates++
			return
		}
		seen[normalized] = true
		pages = append(pages, DiscoveredURL{URL: normalized, Source: source})
		report.TotalQueued = len(pages)
		switch source {
		case "robots":
			report.FromRobots++
		case "sitemap":
			report.FromSitemap++
		case "query-index":
			report.FromQueryIndex++
		}
	}

	for _, sitemapURL := range d.robotsSitemaps(ctx, client, start) {
		found, err := d.fetchDiscoveryEndpoint(ctx, client, sitemapURL, start)
		if err == nil {
			for _, page := range found {
				add(page, "robots")
			}
		}
	}

	for _, path := range []string{"/sitemap.xml", "/sitemap.json"} {
		endpoint := *start
		endpoint.Path = path
		endpoint.RawQuery = ""
		endpoint.Fragment = ""

		found, err := d.fetchDiscoveryEndpoint(ctx, client, endpoint.String(), start)
		if err == nil {
			for _, page := range found {
				add(page, "sitemap")
			}
		}
	}

	queryIndex := *start
	queryIndex.Path = "/query-index.json"
	queryIndex.RawQuery = ""
	queryIndex.Fragment = ""
	if found, err := d.fetchQueryIndexEndpoint(ctx, client, queryIndex.String(), start); err == nil {
		for _, page := range found {
			add(page, "query-index")
		}
	}

	add(start.String(), "root")
	return pages, report
}

func (d Discoverer) robotsSitemaps(ctx context.Context, client *http.Client, root *url.URL) []string {
	robots := *root
	robots.Path = "/robots.txt"
	robots.RawQuery = ""
	robots.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robots.String(), nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil
	}
	return ParseRobotsSitemaps(string(body))
}

func (d Discoverer) fetchQueryIndexEndpoint(ctx context.Context, client *http.Client, endpoint string, root *url.URL) ([]string, error) {
	body, err := d.fetchBody(ctx, client, endpoint)
	if err != nil {
		return nil, err
	}
	pages, total, offset, limit := ParseQueryIndexJSON(body, root)
	if total <= 0 || limit <= 0 || len(pages) >= total {
		return pages, nil
	}
	seenOffsets := map[int]bool{offset: true}
	for nextOffset := offset + limit; nextOffset < total; nextOffset += limit {
		if seenOffsets[nextOffset] {
			break
		}
		seenOffsets[nextOffset] = true
		nextURL, err := url.Parse(endpoint)
		if err != nil {
			break
		}
		query := nextURL.Query()
		query.Set("offset", fmt.Sprintf("%d", nextOffset))
		query.Set("limit", fmt.Sprintf("%d", limit))
		nextURL.RawQuery = query.Encode()
		nextBody, err := d.fetchBody(ctx, client, nextURL.String())
		if err == nil {
			nextPages, _, _, _ := ParseQueryIndexJSON(nextBody, root)
			pages = append(pages, nextPages...)
		}
	}
	return pages, nil
}

func (d Discoverer) fetchBody(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discovery endpoint %s returned %d", endpoint, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
}

func looksLikeAssetOrDownload(raw string, base *url.URL) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	resolved := base.ResolveReference(parsed)
	return isExcludedAssetURL(resolved)
}

func ParseRobotsSitemaps(body string) []string {
	var sitemaps []string
	for _, line := range strings.Split(body, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.ToLower(strings.TrimSpace(parts[0])) != "sitemap" {
			continue
		}
		if value := strings.TrimSpace(parts[1]); value != "" {
			sitemaps = append(sitemaps, value)
		}
	}
	return sitemaps
}

func ParseQueryIndexJSON(body []byte, root *url.URL) ([]string, int, int, int) {
	pages := ParseDiscoveryJSON(body, root)
	var metadata struct {
		Total  int `json:"total"`
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return pages, 0, 0, 0
	}
	return pages, metadata.Total, metadata.Offset, metadata.Limit
}

func (d Discoverer) fetchDiscoveryEndpoint(ctx context.Context, client *http.Client, endpoint string, root *url.URL) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discovery endpoint %s returned %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, err
	}

	if strings.HasSuffix(endpoint, ".xml") {
		pages, childSitemaps := ParseSitemapXML(body)
		for _, child := range childSitemaps {
			childURL, ok := normalizePageURL(child, root)
			if !ok {
				continue
			}
			parsed, err := url.Parse(childURL)
			if err != nil || !sameOrigin(parsed, root) {
				continue
			}
			childPages, err := d.fetchDiscoveryEndpoint(ctx, client, childURL, root)
			if err == nil {
				pages = append(pages, childPages...)
			}
		}
		return pages, nil
	}
	return ParseDiscoveryJSON(body, root), nil
}

func (d Discoverer) linksFromPage(ctx context.Context, client *http.Client, pageURL string, root *url.URL) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fallback page returned %d", resp.StatusCode)
	}
	page, err := AnalyzeHTML(pageURL, io.LimitReader(resp.Body, 16*1024*1024), root)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, link := range page.Links {
		if link.Kind == "internal" {
			urls = append(urls, link.URL)
		}
	}
	return urls, nil
}

func ParseSitemapXML(body []byte) ([]string, []string) {
	type loc struct {
		Loc string `xml:"loc"`
	}
	type urlSet struct {
		URLs []loc `xml:"url"`
	}
	type sitemapIndex struct {
		Sitemaps []loc `xml:"sitemap"`
	}

	var urls urlSet
	var pages []string
	if err := xml.Unmarshal(body, &urls); err == nil {
		for _, entry := range urls.URLs {
			if strings.TrimSpace(entry.Loc) != "" {
				pages = append(pages, strings.TrimSpace(entry.Loc))
			}
		}
	}

	var index sitemapIndex
	var sitemaps []string
	if err := xml.Unmarshal(body, &index); err == nil {
		for _, entry := range index.Sitemaps {
			if strings.TrimSpace(entry.Loc) != "" {
				sitemaps = append(sitemaps, strings.TrimSpace(entry.Loc))
			}
		}
	}
	return pages, sitemaps
}

func ParseDiscoveryJSON(body []byte, root *url.URL) []string {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil
	}

	var pages []string
	var walk func(any, string)
	walk = func(v any, key string) {
		switch node := v.(type) {
		case []any:
			for _, item := range node {
				walk(item, key)
			}
		case map[string]any:
			for k, item := range node {
				walk(item, strings.ToLower(k))
			}
		case string:
			if key == "path" || key == "url" || key == "loc" || strings.Contains(key, "url") {
				if normalized, ok := normalizePageURL(node, root); ok {
					pages = append(pages, normalized)
				}
			}
		}
	}
	walk(value, "")
	return pages
}
