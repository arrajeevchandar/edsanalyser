package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestComparisonPathKeyMatchesAcrossHosts(t *testing.T) {
	cases := map[string]string{
		"https://legacy.example.com/":                  "/",
		"https://legacy.example.com/about/":            "/about",
		"https://main--site--org.aem.live/About#intro": "/about",
		"https://main--site--org.aem.live/p?q=1":       "/p",
	}
	for raw, want := range cases {
		if got := comparisonPathKey(raw); got != want {
			t.Fatalf("comparisonPathKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestBuildComparisonPagesGroupsMatchedMissingAndExtra(t *testing.T) {
	source := []PageResult{
		{URL: "https://legacy.example.com/", Title: "Home"},
		{URL: "https://legacy.example.com/about", Title: "About"},
		{URL: "https://legacy.example.com/broken", FetchError: "HTTP 500"},
	}
	eds := []PageResult{
		{URL: "https://main--site--org.aem.live/", Title: "Home"},
		{URL: "https://main--site--org.aem.live/contact", Title: "Contact"},
		{URL: "https://main--site--org.aem.live/fail", FetchError: "HTTP 404"},
	}
	matched, missing, extra, sourceFailures, edsFailures := buildComparisonPages(source, eds)
	if len(matched) != 1 || matched[0].Path != "/" {
		t.Fatalf("expected home to match, got %+v", matched)
	}
	if len(missing) != 1 || comparisonPathKey(missing[0].URL) != "/about" {
		t.Fatalf("expected /about missing in EDS, got %+v", missing)
	}
	if len(extra) != 1 || comparisonPathKey(extra[0].URL) != "/contact" {
		t.Fatalf("expected /contact extra in EDS, got %+v", extra)
	}
	if len(sourceFailures) != 1 || len(edsFailures) != 1 {
		t.Fatalf("expected one failure per side, got source=%+v eds=%+v", sourceFailures, edsFailures)
	}
}

func TestMetadataAndLinkDiffs(t *testing.T) {
	source := PageResult{
		URL:         "https://legacy.example.com/about",
		Title:       "About",
		H1:          "About us",
		Description: "Legacy description",
		Canonical:   "https://legacy.example.com/about",
		OG:          OpenGraph{Title: "About", URL: "https://legacy.example.com/about"},
		Links: []LinkInfo{
			{URL: "https://legacy.example.com/products", Kind: "internal"},
			{URL: "https://partner.example.com", Kind: "external"},
			{URL: "https://legacy.example.com/media/hero.jpg", Kind: "asset"},
		},
	}
	eds := PageResult{
		URL:         "https://main--site--org.aem.live/about",
		Title:       "About",
		H1:          "Company",
		Description: "Migrated description",
		Canonical:   "https://main--site--org.aem.live/about",
		OG:          OpenGraph{Title: "About", URL: "https://main--site--org.aem.live/about"},
		Links: []LinkInfo{
			{URL: "https://main--site--org.aem.live/contact", Kind: "internal"},
			{URL: "https://main--site--org.aem.live/media/hero-new.jpg", Kind: "asset"},
		},
	}
	fields := metadataDiffs(source, eds)
	if len(fields) != 2 {
		t.Fatalf("expected h1 and description diffs only, got %+v", fields)
	}
	links := linkDiffs(source, eds)
	if len(links) != 5 {
		t.Fatalf("expected missing/added internal, missing external, missing/added asset diffs, got %+v", links)
	}
}

func TestVisualDiffClassification(t *testing.T) {
	if got := classifyVisualDiff(4.99); got != "pass" {
		t.Fatalf("expected pass, got %s", got)
	}
	if got := classifyVisualDiff(5); got != "review" {
		t.Fatalf("expected review, got %s", got)
	}
	if got := classifyVisualDiff(20.01); got != "fail" {
		t.Fatalf("expected fail, got %s", got)
	}
}

func TestStartComparisonRequiresOnlyEDSURLToBeEDS(t *testing.T) {
	sourceMux := http.NewServeMux()
	sourceServer := httptest.NewServer(sourceMux)
	defer sourceServer.Close()
	sourceMux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url></urlset>`, sourceServer.URL)
	})
	sourceMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, pageHTML("Legacy", "/"))
	})

	edsMux := http.NewServeMux()
	edsServer := httptest.NewServer(edsMux)
	defer edsServer.Close()
	edsMux.HandleFunc("/scripts/aem.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, "window.hlx = {};")
	})
	edsMux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url></urlset>`, edsServer.URL)
	})
	edsMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, pageHTML("Migrated", "/"))
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		Lighthouse: NoopLighthouseRunner{},
		Visual:     fakeVisualRunner{},
		Workers:    1,
	})

	opts := DefaultComparisonOptions()
	opts.LighthouseMode = "none"
	comparison, err := service.StartComparison(context.Background(), sourceServer.URL, edsServer.URL, opts)
	if err != nil {
		t.Fatalf("StartComparison returned error: %v", err)
	}
	result := waitForComparison(t, service, comparison.ID, "completed")
	if result.Summary.MatchedPages != 1 {
		t.Fatalf("expected one matched page, got %+v", result.Summary)
	}
}

type fakeVisualRunner struct{}

func (fakeVisualRunner) Diff(_ context.Context, _ string, _ string, _ string, _ string, viewport VisualViewport) VisualDiff {
	return VisualDiff{Viewport: viewport.Name, Status: "pass"}
}

func waitForComparison(t *testing.T, service *Service, id string, status string) ComparisonResult {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var last ComparisonResult
	for {
		result, err := service.GetComparison(id)
		if err == nil {
			last = result
			if result.Summary.Status == status {
				return result
			}
		}
		select {
		case <-deadline:
			t.Fatalf("comparison did not reach %q; last result: %+v", status, last.Summary)
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}
