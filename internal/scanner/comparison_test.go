package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	groups := buildComparisonPages(source, eds)
	if len(groups.Matched) != 1 || groups.Matched[0].Path != "/" {
		t.Fatalf("expected home to match, got %+v", groups.Matched)
	}
	if len(groups.MissingInEDS) != 1 || comparisonPathKey(groups.MissingInEDS[0].URL) != "/about" {
		t.Fatalf("expected /about missing in EDS, got %+v", groups.MissingInEDS)
	}
	if len(groups.ExtraInEDS) != 1 || comparisonPathKey(groups.ExtraInEDS[0].URL) != "/contact" {
		t.Fatalf("expected /contact extra in EDS, got %+v", groups.ExtraInEDS)
	}
	if len(groups.SourceFetchFailures) != 1 || len(groups.EDSFetchFailures) != 1 {
		t.Fatalf("expected one failure per side, got source=%+v eds=%+v", groups.SourceFetchFailures, groups.EDSFetchFailures)
	}
}

func TestBuildComparisonPagesMatchesCanonicalAliasesAsHighConfidence(t *testing.T) {
	source := []PageResult{{URL: "https://legacy.example.com/products/shoe", Title: "Shoe"}}
	eds := []PageResult{{URL: "https://main--site--org.aem.live/shoe", Canonical: "https://main--site--org.aem.live/products/shoe", Title: "Shoe"}}
	groups := buildComparisonPages(source, eds)
	if len(groups.Matched) != 1 {
		t.Fatalf("expected one canonical match, got %+v", groups)
	}
	match := groups.Matched[0]
	if match.MatchType != "canonical" || match.MatchConfidence != "high" || match.Path != "/products/shoe" {
		t.Fatalf("unexpected alias match metadata: %+v", match)
	}
}

func TestBuildComparisonPagesMatchesHTMLExtensionAsExact(t *testing.T) {
	source := []PageResult{
		{URL: "https://legacy.example.com/about.html", Title: "About"},
		{URL: "https://legacy.example.com/products/shoe.html", Title: "Shoe"},
	}
	eds := []PageResult{
		{URL: "https://main--site--org.aem.live/about", Title: "About"},
		{URL: "https://main--site--org.aem.live/products/shoe", Title: "Shoe"},
	}
	groups := buildComparisonPages(source, eds)
	if len(groups.Matched) != 2 {
		t.Fatalf("expected .html pages to match extensionless EDS paths, got matched=%+v missing=%+v", groups.Matched, groups.MissingInEDS)
	}
	for _, match := range groups.Matched {
		if match.MatchType != "exact" || match.MatchConfidence != "high" {
			t.Fatalf("expected exact high-confidence match, got %+v", match)
		}
	}
	if len(groups.MissingInEDS) != 0 || len(groups.ExtraInEDS) != 0 {
		t.Fatalf("expected no missing/extra pages, got missing=%+v extra=%+v", groups.MissingInEDS, groups.ExtraInEDS)
	}
}

func TestBuildComparisonPagesMatchesPathCleanupAsUncertain(t *testing.T) {
	source := []PageResult{{URL: "https://legacy.example.com/en/about.html", Title: "About"}}
	eds := []PageResult{{URL: "https://main--site--org.aem.live/about", Title: "About"}}
	groups := buildComparisonPages(source, eds)
	if len(groups.UncertainMatches) != 1 {
		t.Fatalf("expected one path-cleanup match, got %+v", groups)
	}
	match := groups.UncertainMatches[0]
	if match.MatchConfidence != "medium" || match.Path != "/about" {
		t.Fatalf("unexpected path-cleanup metadata: %+v", match)
	}
}

func TestBuildComparisonPagesSuggestsWeakCandidates(t *testing.T) {
	source := []PageResult{{URL: "https://legacy.example.com/footwear/running-shoe", Title: "Running Shoe", H1: "Running Shoe"}}
	eds := []PageResult{{URL: "https://main--site--org.aem.live/products/running-shoe-new", Title: "Running Shoe", H1: "Running Shoe"}}
	groups := buildComparisonPages(source, eds)
	if len(groups.MissingInEDS) != 1 || len(groups.MissingInEDS[0].MatchCandidates) == 0 {
		t.Fatalf("expected candidate suggestion for missing page, got %+v", groups.MissingInEDS)
	}
	if groups.MissingInEDS[0].MatchCandidates[0].URL != eds[0].URL {
		t.Fatalf("unexpected candidate: %+v", groups.MissingInEDS[0].MatchCandidates)
	}
}

func TestBuildComparisonPagesAppliesManualOverride(t *testing.T) {
	source := []PageResult{{URL: "https://legacy.example.com/old-path", Title: "Legacy"}}
	eds := []PageResult{{URL: "https://main--site--org.aem.live/new-path", Title: "EDS"}}
	groups := buildComparisonPagesWithOverrides(source, eds, []MatchOverride{{SourceURL: source[0].URL, EDSURL: eds[0].URL, Action: "match"}})
	if len(groups.Matched) != 1 || groups.Matched[0].MatchType != "manual" {
		t.Fatalf("expected manual match, got %+v", groups)
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
		Rendered:   fakeRenderedLinkExtractor{},
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

func TestComparisonCrawlsSameOriginLinksBeyondHome(t *testing.T) {
	sourceMux := http.NewServeMux()
	sourceServer := httptest.NewServer(sourceMux)
	defer sourceServer.Close()
	sourceMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, pageHTML("Legacy Home", "/about"), `<a href="/products">Products</a>`)
		case "/about":
			fmt.Fprint(w, pageHTML("Legacy About", "/contact"))
		case "/products":
			fmt.Fprint(w, pageHTML("Legacy Products", "/"))
		case "/contact":
			fmt.Fprint(w, pageHTML("Legacy Contact", "/"))
		default:
			http.NotFound(w, r)
		}
	})

	edsMux := http.NewServeMux()
	edsServer := httptest.NewServer(edsMux)
	defer edsServer.Close()
	edsMux.HandleFunc("/scripts/aem.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, "export default function decorate(){}")
	})
	edsMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, pageHTML("Migrated Home", "/about"), `<a href="/products">Products</a><a href="/extra">Extra</a>`)
		case "/about":
			fmt.Fprint(w, pageHTML("Migrated About", "/contact"))
		case "/products":
			fmt.Fprint(w, pageHTML("Migrated Products", "/"))
		case "/contact":
			fmt.Fprint(w, pageHTML("Migrated Contact", "/"))
		case "/extra":
			fmt.Fprint(w, pageHTML("Migrated Extra", "/"))
		default:
			http.NotFound(w, r)
		}
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		Lighthouse: NoopLighthouseRunner{},
		Visual:     fakeVisualRunner{},
		Rendered:   fakeRenderedLinkExtractor{},
		Workers:    2,
	})

	opts := DefaultComparisonOptions()
	opts.LighthouseMode = "none"
	comparison, err := service.StartComparison(context.Background(), sourceServer.URL, edsServer.URL, opts)
	if err != nil {
		t.Fatalf("StartComparison returned error: %v", err)
	}
	result := waitForComparison(t, service, comparison.ID, "completed")
	if result.Summary.MatchedPages != 4 {
		t.Fatalf("expected four matched pages from link expansion, got %+v", result.Summary)
	}
	if result.Summary.ExtraInEDS != 1 || len(result.ExtraInEDS) != 1 {
		t.Fatalf("expected one EDS-only page, got summary=%+v extra=%+v", result.Summary, result.ExtraInEDS)
	}
	// Default visual diff runs for every matched pair (4 pages) across desktop and
	// mobile, capped by autoVisualLimit.
	if result.Summary.VisualQueued != 8 || result.Summary.VisualCompleted != 8 {
		t.Fatalf("default comparison should run visual diff for all matched pages desktop/mobile, got %+v", result.Summary)
	}
	if result.Discovery.Source.FromStaticLinks < 3 || result.Discovery.EDS.FromStaticLinks < 4 {
		t.Fatalf("expected static link discovery to expand both sites, got %+v", result.Discovery)
	}
}

func TestUpdateComparisonMatchPersistsManualOverride(t *testing.T) {
	sourceMux := http.NewServeMux()
	sourceServer := httptest.NewServer(sourceMux)
	defer sourceServer.Close()
	sourceMux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url><url><loc>%s/old-path</loc></url></urlset>`, sourceServer.URL, sourceServer.URL)
	})
	sourceMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old-path" {
			fmt.Fprint(w, pageHTML("Legacy Product", "/"))
			return
		}
		fmt.Fprint(w, pageHTML("Legacy Home", "/old-path"))
	})

	edsMux := http.NewServeMux()
	edsServer := httptest.NewServer(edsMux)
	defer edsServer.Close()
	edsMux.HandleFunc("/scripts/aem.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, "export default function decorate(){}")
	})
	edsMux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url><url><loc>%s/new-path</loc></url></urlset>`, edsServer.URL, edsServer.URL)
	})
	edsMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/new-path" {
			fmt.Fprint(w, pageHTML("Migrated Product", "/"))
			return
		}
		fmt.Fprint(w, pageHTML("Migrated Home", "/new-path"))
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		Lighthouse: NoopLighthouseRunner{},
		Visual:     fakeVisualRunner{},
		Rendered:   fakeRenderedLinkExtractor{},
		Workers:    2,
	})

	opts := DefaultComparisonOptions()
	opts.LighthouseMode = "none"
	comparison, err := service.StartComparison(context.Background(), sourceServer.URL, edsServer.URL, opts)
	if err != nil {
		t.Fatalf("StartComparison returned error: %v", err)
	}
	result := waitForComparison(t, service, comparison.ID, "completed")
	if result.Summary.MissingInEDS != 1 || result.Summary.ExtraInEDS != 1 {
		t.Fatalf("expected one missing and one extra before override, got %+v", result.Summary)
	}
	updated, err := service.UpdateComparisonMatch(comparison.ID, MatchOverride{SourceURL: sourceServer.URL + "/old-path", EDSURL: edsServer.URL + "/new-path", Action: "match"})
	if err != nil {
		t.Fatalf("UpdateComparisonMatch returned error: %v", err)
	}
	if updated.Summary.MissingInEDS != 0 || updated.Summary.ExtraInEDS != 0 || updated.Summary.MatchedPages != 2 {
		t.Fatalf("manual override did not recompute groups: %+v", updated.Summary)
	}
}

func TestComparisonUsesRenderedDiscoveryFallback(t *testing.T) {
	sourceMux := http.NewServeMux()
	sourceServer := httptest.NewServer(sourceMux)
	defer sourceServer.Close()
	sourceMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, scriptedPageHTML("Legacy Home"))
		case "/about":
			fmt.Fprint(w, pageHTML("Legacy About", "/"))
		case "/products":
			fmt.Fprint(w, pageHTML("Legacy Products", "/"))
		default:
			http.NotFound(w, r)
		}
	})

	edsMux := http.NewServeMux()
	edsServer := httptest.NewServer(edsMux)
	defer edsServer.Close()
	edsMux.HandleFunc("/scripts/aem.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, "export default function decorate(){}")
	})
	edsMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, scriptedPageHTML("Migrated Home"))
		case "/about":
			fmt.Fprint(w, pageHTML("Migrated About", "/"))
		case "/products":
			fmt.Fprint(w, pageHTML("Migrated Products", "/"))
		default:
			http.NotFound(w, r)
		}
	})

	rendered := fakeRenderedLinkExtractor{links: map[string][]string{
		sourceServer.URL + "/": {sourceServer.URL + "/about", sourceServer.URL + "/products"},
		edsServer.URL + "/":    {edsServer.URL + "/about", edsServer.URL + "/products"},
	}}
	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		Lighthouse: NoopLighthouseRunner{},
		Visual:     fakeVisualRunner{},
		Rendered:   rendered,
		Workers:    2,
	})

	opts := DefaultComparisonOptions()
	opts.LighthouseMode = "none"
	comparison, err := service.StartComparison(context.Background(), sourceServer.URL, edsServer.URL, opts)
	if err != nil {
		t.Fatalf("StartComparison returned error: %v", err)
	}
	result := waitForComparison(t, service, comparison.ID, "completed")
	if result.Summary.MatchedPages != 3 {
		t.Fatalf("expected rendered fallback to discover three matched pages, got %+v", result.Summary)
	}
	if result.Discovery.Source.FromRenderedLinks != 2 || result.Discovery.EDS.FromRenderedLinks != 2 {
		t.Fatalf("expected rendered discovery counts, got %+v", result.Discovery)
	}
	if len(result.Discovery.Source.Warnings) == 0 || len(result.Discovery.EDS.Warnings) == 0 {
		t.Fatalf("expected rendered discovery warnings, got %+v", result.Discovery)
	}
}

type fakeVisualRunner struct{}

func (fakeVisualRunner) Diff(_ context.Context, _ string, _ string, _ string, _ string, viewport VisualViewport) VisualDiff {
	return VisualDiff{Viewport: viewport.Name, Status: "pass"}
}

type fakeRenderedLinkExtractor struct {
	links map[string][]string
}

func (f fakeRenderedLinkExtractor) Links(_ context.Context, pageURL string, _ *url.URL) ([]string, error) {
	return f.links[pageURL], nil
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

func scriptedPageHTML(title string) string {
	return fmt.Sprintf(`
<!doctype html>
<html lang="en">
  <head>
    <title>%s</title>
    <meta name="description" content="%s description" />
    <meta property="og:title" content="%s" />
  </head>
  <body>
    <main>
      <div class="section default"><div class="hero primary"><h1>%s</h1></div></div>
    </main>
    <script>window.__nav = ["/about", "/products"];</script>
  </body>
</html>`, title, title, title, title)
}
