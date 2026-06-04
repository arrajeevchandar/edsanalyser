package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestParseRobotsSitemaps(t *testing.T) {
	got := ParseRobotsSitemaps("User-agent: *\nSitemap: https://example.com/sitemap.xml\nsitemap: https://example.com/news.xml\n")
	if len(got) != 2 || got[0] != "https://example.com/sitemap.xml" || got[1] != "https://example.com/news.xml" {
		t.Fatalf("unexpected sitemaps: %+v", got)
	}
}

func TestDiscoverDetailedUsesRobotsAndPaginatedQueryIndex(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "Sitemap: %s/robots-sitemap.xml\n", server.URL)
	})
	mux.HandleFunc("/robots-sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/from-robots</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<sitemapindex><sitemap><loc>%s/child.xml</loc></sitemap></sitemapindex>`, server.URL)
	})
	mux.HandleFunc("/child.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/from-sitemap</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/query-index.json", func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		if offset == "2" {
			fmt.Fprintf(w, `{"total":3,"offset":2,"limit":2,"data":[{"path":"/from-query-3"}]}`)
			return
		}
		fmt.Fprintf(w, `{"total":3,"offset":0,"limit":2,"data":[{"path":"/from-query-1"},{"path":"/from-query-2"}]}`)
	})

	root, _ := url.Parse(server.URL)
	seeds, report := (Discoverer{Client: server.Client()}).DiscoverDetailed(context.Background(), root)
	seen := map[string]bool{}
	for _, seed := range seeds {
		seen[seed.URL] = true
	}
	for _, path := range []string{"/from-robots", "/from-sitemap", "/from-query-1", "/from-query-2", "/from-query-3", "/"} {
		if !seen[server.URL+path] {
			t.Fatalf("expected %s in seeds %+v", path, seeds)
		}
	}
	if report.FromRobots != 1 || report.FromSitemap != 1 || report.FromQueryIndex != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
}
