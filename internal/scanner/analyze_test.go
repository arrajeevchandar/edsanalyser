package scanner

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeInputURL(t *testing.T) {
	parsed, err := NormalizeInputURL("example.com/docs#intro")
	if err != nil {
		t.Fatalf("NormalizeInputURL returned error: %v", err)
	}
	if parsed.String() != "https://example.com/docs" {
		t.Fatalf("unexpected normalized URL: %s", parsed.String())
	}

	if _, err := NormalizeInputURL("ftp://example.com"); err == nil {
		t.Fatal("expected invalid scheme error")
	}
}

func TestParseSitemapXML(t *testing.T) {
	pages, sitemaps := ParseSitemapXML([]byte(`
<sitemapindex>
  <sitemap><loc>https://example.com/sitemap-pages.xml</loc></sitemap>
</sitemapindex>`))
	if len(pages) != 0 {
		t.Fatalf("expected no pages, got %v", pages)
	}
	if len(sitemaps) != 1 || sitemaps[0] != "https://example.com/sitemap-pages.xml" {
		t.Fatalf("unexpected sitemaps: %v", sitemaps)
	}

	pages, _ = ParseSitemapXML([]byte(`
<urlset>
  <url><loc>https://example.com/</loc></url>
  <url><loc>https://example.com/about</loc></url>
</urlset>`))
	if len(pages) != 2 {
		t.Fatalf("expected two pages, got %v", pages)
	}
}

func TestParseDiscoveryJSON(t *testing.T) {
	root, _ := url.Parse("https://example.com/")
	pages := ParseDiscoveryJSON([]byte(`{"data":[{"path":"/about"},{"url":"https://example.com/products"}]}`), root)
	if len(pages) != 2 {
		t.Fatalf("expected two pages, got %v", pages)
	}
}

func TestAnalyzeHTMLExtractsEDSSEOAndLinks(t *testing.T) {
	root, _ := url.Parse("https://example.com/")
	html := `
<!doctype html>
<html lang="en">
  <head>
    <title>Home | Example</title>
    <link rel="canonical" href="https://example.com/" />
    <meta name="description" content="A useful page" />
    <meta property="og:title" content="OG Home" />
    <meta property="og:image" content="https://example.com/og.png" />
    <meta property="og:url" content="https://example.com/" />
  </head>
  <body>
    <main>
      <div class="section hero-area">
        <div class="hero spotlight"><h1>Welcome</h1></div>
        <div class="section-metadata"><div><div>Style</div><div>dark wide</div></div></div>
      </div>
      <div>
        <div class="cards three-up"></div>
      </div>
    </main>
    <a href="/about">About</a>
    <a href="https://adobe.com">Adobe</a>
    <a href="mailto:hello@example.com">Email</a>
  </body>
</html>`
	page, err := AnalyzeHTML("https://example.com/", strings.NewReader(html), root)
	if err != nil {
		t.Fatalf("AnalyzeHTML returned error: %v", err)
	}
	if page.Title != "Home | Example" || page.H1 != "Welcome" || page.OG.Title != "OG Home" {
		t.Fatalf("metadata was not extracted: %+v", page)
	}
	if page.SectionCount != 2 || page.BlockCount != 2 {
		t.Fatalf("unexpected EDS counts: sections=%d blocks=%d", page.SectionCount, page.BlockCount)
	}
	if page.InternalLinks != 1 || page.ExternalLinks != 1 || page.LinkCount != 3 {
		t.Fatalf("unexpected link counts: %+v", page.Links)
	}
	if !contains(page.Sections[0].Variations, "dark") || !contains(page.Sections[0].Variations, "wide") {
		t.Fatalf("section metadata variations missing: %+v", page.Sections[0].Variations)
	}
}

func TestAnalyzeHTMLExtractsRenderedMediaAsAssets(t *testing.T) {
	root, _ := url.Parse("https://example.com/")
	html := `
<!doctype html>
<html lang="en">
  <body>
    <main>
      <picture>
        <source type="image/webp" srcset="./media_hero.png?width=750&format=webply 750w, ./media_hero.png?width=2000&format=webply 2000w" />
        <img src="./media_hero.png?width=750&format=png" alt="Hero banner" />
      </picture>
      <img src="/assets/logo.svg" alt="Logo" />
      <video src="/media/clip.mp4" poster="/media/poster.jpg"></video>
      <audio src="/media/song.mp3"></audio>
      <div style="background-image: url('/media/backdrop.webp')"></div>
      <img data-src="/lazy/photo.jpeg" />
      <img src="data:image/gif;base64,R0lGODlh" alt="inline" />
      <a href="/files/brochure.pdf">Download</a>
    </main>
  </body>
</html>`
	page, err := AnalyzeHTML("https://example.com/", strings.NewReader(html), root)
	if err != nil {
		t.Fatalf("AnalyzeHTML returned error: %v", err)
	}

	assets := map[string]bool{}
	assetText := map[string]string{}
	for _, link := range page.Links {
		if link.Kind == "asset" {
			assets[link.URL] = true
			assetText[link.URL] = link.Text
		}
	}

	// Each media file is named by its file name.
	if got := assetText["https://example.com/media_hero.png"]; got != "media_hero.png" {
		t.Fatalf("expected hero asset name to be its file name, got %q", got)
	}

	// All media (images, audio, video, css background, lazy data-src, linked file)
	// must be captured by extension, and the responsive <picture> must collapse to
	// a single hero asset.
	want := []string{
		"https://example.com/media_hero.png",
		"https://example.com/assets/logo.svg",
		"https://example.com/media/clip.mp4",
		"https://example.com/media/poster.jpg",
		"https://example.com/media/song.mp3",
		"https://example.com/media/backdrop.webp",
		"https://example.com/lazy/photo.jpeg",
		"https://example.com/files/brochure.pdf",
	}
	for _, url := range want {
		if !assets[url] {
			t.Fatalf("expected asset %q in %+v", url, assets)
		}
	}
	if len(assets) != len(want) {
		t.Fatalf("expected %d unique assets, got %d: %+v", len(want), len(assets), assets)
	}
	// data: URIs are not real downloadable assets and must be ignored.
	if assets["data:image/gif;base64,R0lGODlh"] {
		t.Fatal("data: URI should not be counted as an asset")
	}
}

func TestAnalyzeHTMLResolvesRelativeLinksAgainstCurrentPage(t *testing.T) {
	root, _ := url.Parse("https://example.com/")
	page, err := AnalyzeHTML("https://example.com/docs/index", strings.NewReader(`<a href="child">Child</a>`), root)
	if err != nil {
		t.Fatalf("AnalyzeHTML returned error: %v", err)
	}
	if len(page.Links) != 1 {
		t.Fatalf("expected one link, got %+v", page.Links)
	}
	if page.Links[0].URL != "https://example.com/docs/child" {
		t.Fatalf("relative link resolved incorrectly: %s", page.Links[0].URL)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
