package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceScansFixtureSiteFromSitemap(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url><url><loc>%s/about</loc></url></urlset>`, server.URL, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("Home", "/about"))
	})
	mux.HandleFunc("/about", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("About", "/"))
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: fixedLighthouse{},
		Workers:    2,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")
	if result.Summary.CompletedPages != 2 {
		t.Fatalf("expected two completed pages, got %+v", result.Summary)
	}
	if len(result.Pages) != 2 || len(result.Blocks) == 0 {
		t.Fatalf("expected persisted page and block details, got %+v", result)
	}
	if result.Summary.Scores.Health == nil || *result.Summary.Scores.Health != 91 {
		t.Fatalf("unexpected health score: %+v", result.Summary.Scores)
	}
}

func TestServiceCancelsSlowScan(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/slow</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			fmt.Fprint(w, pageHTML("Slow", "/slow"))
		case <-r.Context().Done():
		}
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: fixedLighthouse{},
		Workers:    1,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	if err := service.CancelScan(scan.ID); err != nil {
		t.Fatalf("CancelScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "cancelled")
	if result.Summary.Status != "cancelled" {
		t.Fatalf("expected cancelled scan, got %s", result.Summary.Status)
	}
}

func TestFastAnalysisPersistsBeforeLighthouseFinishes(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("Home", "/"))
	})

	store := openTestStore(t)
	defer store.Close()
	runner := &blockingLighthouse{started: make(chan string, 1), release: make(chan struct{})}
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: runner,
		Workers:    1,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("lighthouse did not start")
	}

	result, err := service.GetScan(scan.ID)
	if err != nil {
		t.Fatalf("GetScan returned error: %v", err)
	}
	if result.Summary.FastCompletedPages != 1 || len(result.Pages) != 1 {
		t.Fatalf("expected fast page results before lighthouse completion, got %+v", result)
	}
	if result.Pages[0].AuditStatus != "running" {
		t.Fatalf("expected running audit status, got %s", result.Pages[0].AuditStatus)
	}
	close(runner.release)
	waitForScan(t, service, scan.ID, "completed")
}

func TestDefaultLighthouseLimitAuditsTopFivePages(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<urlset>`)
		for i := 0; i < 7; i++ {
			fmt.Fprintf(w, `<url><loc>%s/page-%d</loc></url>`, server.URL, i)
		}
		fmt.Fprint(w, `</urlset>`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, pageHTML(r.URL.Path, "/page-0"))
	})

	store := openTestStore(t)
	defer store.Close()
	runner := &countingLighthouse{}
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: runner,
		Workers:    3,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")
	if got := runner.count.Load(); got != 5 {
		t.Fatalf("expected 5 lighthouse audits, got %d", got)
	}
	if result.Summary.AuditQueuedPages != 5 || result.Summary.AuditCompletedPages != 5 {
		t.Fatalf("unexpected audit counters: %+v", result.Summary)
	}
}

func TestLighthouseFailureDoesNotIncrementPageFailures(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("Home", "/"))
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: failingLighthouse{},
		Workers:    1,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")
	if result.Summary.FailedPages != 0 {
		t.Fatalf("lighthouse failure should not count as page failure: %+v", result.Summary)
	}
	if result.Summary.AuditFailedPages != 1 {
		t.Fatalf("expected one audit failure: %+v", result.Summary)
	}
}

func TestStoreNormalizesNullJSONFields(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	scan := ScanSummary{
		ID:        "scan-null-json",
		InputURL:  "https://example.com",
		RootURL:   "https://example.com/",
		Status:    "completed",
		Phase:     "completed",
		StartedAt: time.Now(),
	}
	if err := store.CreateScan(scan); err != nil {
		t.Fatalf("CreateScan returned error: %v", err)
	}
	_, err := store.db.Exec(`
INSERT INTO pages (
  scan_id, url, status_code, title, h1, canonical, description, robots, lang,
  og_json, links_json, blocks_json, sections_json, block_count, section_count, link_count,
  internal_links, external_links, audit_status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		scan.ID, "https://example.com/", 200, "Home", "", "", "", "", "",
		`{}`, `null`, `null`, `null`, 0, 0, 0, 0, 0, "")
	if err != nil {
		t.Fatalf("insert null page returned error: %v", err)
	}

	result, err := store.GetScan(scan.ID)
	if err != nil {
		t.Fatalf("GetScan returned error: %v", err)
	}
	if len(result.Pages) != 1 || result.Pages[0].Links == nil || result.Pages[0].Blocks == nil || result.Pages[0].Sections == nil {
		t.Fatalf("page fields were not normalized: %+v", result.Pages)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json marshal returned error: %v", err)
	}
	for _, forbidden := range []string{`"pages":null`, `"links":null`, `"blocks":null`, `"sections":null`, `"variations":null`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("response still contains %s: %s", forbidden, payload)
		}
	}
}

func TestServiceDeduplicatesRedirectTargets(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url><url><loc>%s/new</loc></url><url><loc>%s/old</loc></url></urlset>`,
			server.URL, server.URL, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, pageHTML("Home", "/new"))
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, pageHTML("New", "/"))
	})
	// /old permanently redirects to /new, so it must not appear as its own page.
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/new", http.StatusMovedPermanently)
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: fixedLighthouse{},
		Workers:    2,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")

	urls := map[string]int{}
	for _, page := range result.Pages {
		urls[page.URL]++
	}
	if len(result.Pages) != 2 {
		t.Fatalf("expected 2 unique pages after redirect dedupe, got %d: %+v", len(result.Pages), urls)
	}
	if urls[server.URL+"/new"] != 1 {
		t.Fatalf("expected /new exactly once, got %+v", urls)
	}
	if result.Summary.DiscoveredPages != 2 {
		t.Fatalf("expected discovered count to reflect unique pages (2), got %d", result.Summary.DiscoveredPages)
	}
}

func TestServiceCollapsesCanonicalQueryVariants(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset>
			<url><loc>%s/</loc></url>
			<url><loc>%s/mens</loc></url>
			<url><loc>%s/mens?category=running</loc></url>
			<url><loc>%s/mens?category=casual</loc></url>
		</urlset>`, server.URL, server.URL, server.URL, server.URL)
	})
	// Go's ServeMux ignores the query string, so every /mens* request is served
	// the same page, which declares /mens as its canonical URL.
	mux.HandleFunc("/mens", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, canonicalPageHTML("Mens", server.URL+"/mens", "/"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, pageHTML("Home", "/mens"))
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: fixedLighthouse{},
		Workers:    3,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")

	mens := 0
	for _, page := range result.Pages {
		if strings.Contains(page.URL, "/mens") {
			mens++
			if strings.Contains(page.URL, "?") {
				t.Fatalf("query-string variant was not collapsed: %s", page.URL)
			}
		}
	}
	if mens != 1 {
		urls := make([]string, 0, len(result.Pages))
		for _, page := range result.Pages {
			urls = append(urls, page.URL)
		}
		t.Fatalf("expected the /mens variants to collapse to 1 page, got %d: %v", mens, urls)
	}
}

func TestSelectAuditPagesIncludesHomeAndContentHeavy(t *testing.T) {
	// Home is deliberately not first and intentionally light, and two utility
	// pages (0 blocks) are included to ensure they never outrank real content.
	pages := []PageResult{
		{URL: "https://x.com/nav", BlockCount: 0, SectionCount: 4},
		{URL: "https://x.com/a", BlockCount: 10}, // heaviest
		{URL: "https://x.com/", BlockCount: 1},   // home
		{URL: "https://x.com/fragments/shell", BlockCount: 0, SectionCount: 1},
		{URL: "https://x.com/b", BlockCount: 8},
		{URL: "https://x.com/c", BlockCount: 6},
	}
	selected := selectAuditPages(pages, ScanOptions{LighthouseMode: "top", LighthouseLimit: 3}, "https://x.com/")
	if len(selected) != 3 {
		t.Fatalf("expected 3 selected pages, got %d: %+v", len(selected), selected)
	}
	picked := map[string]bool{}
	for _, page := range selected {
		picked[page.URL] = true
	}
	if !picked["https://x.com/"] {
		t.Fatalf("home page must always be included: %+v", picked)
	}
	if !picked["https://x.com/a"] || !picked["https://x.com/b"] {
		t.Fatalf("the two most content-heavy pages must be included: %+v", picked)
	}
	if picked["https://x.com/nav"] || picked["https://x.com/fragments/shell"] {
		t.Fatalf("content-light utility pages must not be selected over real content: %+v", picked)
	}
}

func TestSelectAuditPagesAllModeSkipsFetchErrors(t *testing.T) {
	pages := []PageResult{
		{URL: "https://x.com/a"},
		{URL: "https://x.com/b", FetchError: "HTTP 404"},
		{URL: "https://x.com/c"},
	}
	selected := selectAuditPages(pages, ScanOptions{LighthouseMode: "all"}, "https://x.com/")
	if len(selected) != 2 {
		t.Fatalf("all mode should audit every fetched page, got %d: %+v", len(selected), selected)
	}
	for _, page := range selected {
		if page.FetchError != "" {
			t.Fatalf("fetch-error pages must be skipped: %+v", page)
		}
	}
}

func TestReauditScanAuditsAllPages(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url><url><loc>%s/a</loc></url><url><loc>%s/b</loc></url></urlset>`,
			server.URL, server.URL, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, pageHTML(r.URL.Path, "/a"))
	})

	store := openTestStore(t)
	defer store.Close()
	runner := &countingLighthouse{}
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: runner,
		Workers:    2,
	})

	opts := DefaultScanOptions()
	opts.LighthouseMode = "none"
	scan, err := service.StartScan(context.Background(), server.URL, opts)
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")
	if got := runner.count.Load(); got != 0 {
		t.Fatalf("expected no audits on a none-mode scan, got %d", got)
	}
	pageCount := len(result.Pages)
	if pageCount == 0 {
		t.Fatalf("expected pages to be crawled before re-audit")
	}

	if _, err := service.ReauditScan(scan.ID, ScanOptions{LighthouseMode: "all"}); err != nil {
		t.Fatalf("ReauditScan returned error: %v", err)
	}
	result = waitForScan(t, service, scan.ID, "completed")
	if got := int(runner.count.Load()); got != pageCount {
		t.Fatalf("expected re-audit to cover all %d pages, audited %d", pageCount, got)
	}
	if result.Summary.AuditCompletedPages != pageCount {
		t.Fatalf("expected %d audit completions, got %+v", pageCount, result.Summary)
	}
	if result.Summary.Scores.Health == nil {
		t.Fatalf("expected aggregated health score after re-audit: %+v", result.Summary.Scores)
	}
}

func TestPageIdentityCollapsesQueryVariantsByCanonical(t *testing.T) {
	root, _ := url.Parse("https://x.com/")
	cases := []struct {
		name string
		page PageResult
		want string
	}{
		{
			name: "query variant collapses to canonical path",
			page: PageResult{URL: "https://x.com/mens?category=running", Canonical: "https://x.com/mens"},
			want: "https://x.com/mens",
		},
		{
			name: "different-path canonical is not collapsed",
			page: PageResult{URL: "https://x.com/womens", Canonical: "https://x.com/"},
			want: "https://x.com/womens",
		},
		{
			name: "no canonical keeps own url",
			page: PageResult{URL: "https://x.com/about"},
			want: "https://x.com/about",
		},
		{
			name: "cross-origin canonical is ignored",
			page: PageResult{URL: "https://x.com/p", Canonical: "https://other.com/p"},
			want: "https://x.com/p",
		},
	}
	for _, tc := range cases {
		if got := pageIdentity(tc.page, root); got != tc.want {
			t.Fatalf("%s: pageIdentity = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLiteStore returned error: %v", err)
	}
	return store
}

func waitForScan(t *testing.T, service *Service, id string, status string) ScanResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last ScanResult
	for time.Now().Before(deadline) {
		result, err := service.GetScan(id)
		if err == nil {
			last = result
			if result.Summary.Status == status {
				return result
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("scan did not reach %q; last result: %+v", status, last.Summary)
	return ScanResult{}
}

func pageHTML(title, link string) string {
	return fmt.Sprintf(`
<!doctype html>
<html lang="en">
  <head>
    <title>%s</title>
    <meta name="description" content="%s description" />
    <meta property="og:title" content="%s" />
    <meta property="og:image" content="/og.png" />
    <meta property="og:url" content="/" />
  </head>
  <body>
    <main>
      <div class="section default"><div class="hero primary"><h1>%s</h1></div></div>
    </main>
    <a href="%s">Next</a>
  </body>
</html>`, title, title, title, title, link)
}

func canonicalPageHTML(title, canonical, link string) string {
	return fmt.Sprintf(`
<!doctype html>
<html lang="en">
  <head>
    <title>%s</title>
    <link rel="canonical" href="%s" />
    <meta property="og:title" content="%s" />
  </head>
  <body>
    <main>
      <div class="section default"><div class="hero primary"><h1>%s</h1></div></div>
    </main>
    <a href="%s">Next</a>
  </body>
</html>`, title, canonical, title, title, link)
}

type fixedLighthouse struct{}

func (fixedLighthouse) Audit(context.Context, string) (ScoreSet, error) {
	performance := 90.0
	accessibility := 92.0
	bestPractices := 88.0
	seo := 94.0
	health := 91.0
	return ScoreSet{
		Performance:   &performance,
		Accessibility: &accessibility,
		BestPractices: &bestPractices,
		SEO:           &seo,
		Health:        &health,
	}, nil
}

type blockingLighthouse struct {
	started chan string
	release chan struct{}
}

func (b *blockingLighthouse) Audit(ctx context.Context, pageURL string) (ScoreSet, error) {
	b.started <- pageURL
	select {
	case <-b.release:
		return fixedLighthouse{}.Audit(ctx, pageURL)
	case <-ctx.Done():
		return ScoreSet{}, ctx.Err()
	}
}

type countingLighthouse struct {
	count atomic.Int32
}

func (c *countingLighthouse) Audit(ctx context.Context, pageURL string) (ScoreSet, error) {
	c.count.Add(1)
	return fixedLighthouse{}.Audit(ctx, pageURL)
}

type failingLighthouse struct{}

func (failingLighthouse) Audit(context.Context, string) (ScoreSet, error) {
	return ScoreSet{}, errors.New("lighthouse failed")
}
