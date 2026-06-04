package scanner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type ServiceOptions struct {
	HTTPClient *http.Client
	Lighthouse LighthouseRunner
	Visual     VisualRunner
	Rendered   RenderedLinkExtractor
	Workers    int
}

type ScanOptions struct {
	CrawlLimit      *int
	LighthouseMode  string
	LighthouseLimit int
}

func DefaultScanOptions() ScanOptions {
	return ScanOptions{
		LighthouseMode:  "top",
		LighthouseLimit: 5,
	}
}

type Service struct {
	store      Store
	client     *http.Client
	lighthouse LighthouseRunner
	visual     VisualRunner
	rendered   RenderedLinkExtractor
	workers    int

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	events  map[string][]chan Event
}

func NewService(store Store, opts ServiceOptions) *Service {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	lighthouse := opts.Lighthouse
	if lighthouse == nil {
		lighthouse = NoopLighthouseRunner{}
	}
	visual := opts.Visual
	if visual == nil {
		visual = NewChromeVisualRunner()
	}
	rendered := opts.Rendered
	if rendered == nil {
		rendered = ChromeRenderedLinkExtractor{Timeout: 15 * time.Second}
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	return &Service{
		store:      store,
		client:     client,
		lighthouse: lighthouse,
		visual:     visual,
		rendered:   rendered,
		workers:    workers,
		cancels:    map[string]context.CancelFunc{},
		events:     map[string][]chan Event{},
	}
}

func (s *Service) StartScan(parent context.Context, inputURL string, opts ScanOptions) (ScanSummary, error) {
	root, err := NormalizeInputURL(inputURL)
	if err != nil {
		return ScanSummary{}, err
	}
	if opts.CrawlLimit != nil && *opts.CrawlLimit <= 0 {
		opts.CrawlLimit = nil
	}
	if opts.LighthouseMode == "" {
		opts.LighthouseMode = "top"
	}
	if opts.LighthouseLimit <= 0 {
		opts.LighthouseLimit = 5
	}

	id := newID()
	ctx, cancel := context.WithCancel(context.Background())
	scan := ScanSummary{
		ID:        id,
		InputURL:  inputURL,
		RootURL:   root.String(),
		Status:    "running",
		Phase:     "discovering",
		StartedAt: time.Now(),
	}
	if err := s.store.CreateScan(scan); err != nil {
		cancel()
		return ScanSummary{}, err
	}

	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()

	go s.runScan(ctx, scan, root, opts)
	return scan, parent.Err()
}

// CheckEDS reports whether the given site looks like an Adobe Edge Delivery
// Services (EDS) project by probing for /scripts/aem.js at the site origin,
// which every EDS site ships. It returns the normalized root URL alongside the
// result so the caller can surface what was actually checked.
func (s *Service) CheckEDS(ctx context.Context, inputURL string) (bool, string, error) {
	root, err := NormalizeInputURL(inputURL)
	if err != nil {
		return false, "", err
	}
	probe := root.Scheme + "://" + root.Host + "/scripts/aem.js"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
	if err != nil {
		return false, root.String(), err
	}
	req.Header.Set("User-Agent", "EDSAnalyser/0.1 (+https://localhost)")
	resp, err := s.client.Do(req)
	if err != nil {
		return false, root.String(), err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	isEDS := isEDSScriptResponse(resp.StatusCode, resp.Header.Get("Content-Type"), body)
	return isEDS, root.String(), nil
}

// isEDSScriptResponse decides whether a /scripts/aem.js probe response is a real
// EDS script rather than a catch-all fallback. SPA hosts like Netlify answer
// every path with a 200 and the site's index.html, so the status code alone is
// not enough: the response must actually be JavaScript and must not be an HTML
// page.
func isEDSScriptResponse(status int, contentType string, body []byte) bool {
	if status < 200 || status >= 300 {
		return false
	}
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "html") {
		return false
	}
	snippet := strings.ToLower(strings.TrimSpace(string(body)))
	if strings.HasPrefix(snippet, "<!doctype") || strings.HasPrefix(snippet, "<html") || strings.Contains(snippet, "<head") {
		return false
	}
	return strings.Contains(ct, "javascript") || strings.Contains(ct, "ecmascript")
}

func (s *Service) ListScans() ([]ScanSummary, error) {
	return s.store.ListScans()
}

func (s *Service) GetScan(id string) (ScanResult, error) {
	return s.store.GetScan(id)
}

func (s *Service) CancelScan(id string) error {
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel == nil {
		return errors.New("scan is not running")
	}
	cancel()
	s.publish(Event{Type: "cancel", ScanID: id, Message: "Scan cancellation requested"})
	return nil
}

// ReauditScan re-runs Lighthouse on an existing scan without re-crawling. With
// the default options (LighthouseMode "all") it audits every fetched page, which
// backs the "Run Lighthouse for all pages" action in the dashboard.
func (s *Service) ReauditScan(id string, opts ScanOptions) (ScanSummary, error) {
	result, err := s.store.GetScan(id)
	if err != nil {
		return ScanSummary{}, err
	}
	if opts.LighthouseMode == "" {
		opts.LighthouseMode = "all"
	}

	s.mu.Lock()
	if _, running := s.cancels[id]; running {
		s.mu.Unlock()
		return ScanSummary{}, errors.New("scan is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[id] = cancel
	s.mu.Unlock()

	scan := result.Summary
	scan.Status = "running"
	scan.Phase = "auditing"
	scan.AuditQueuedPages = 0
	scan.AuditCompletedPages = 0
	scan.AuditFailedPages = 0
	scan.Scores = ScoreSet{}
	scan.Error = ""
	scan.FinishedAt = time.Time{}
	_ = s.store.UpdateScan(scan)

	go s.runReaudit(ctx, scan, result.Pages, opts)
	return scan, nil
}

func (s *Service) runReaudit(ctx context.Context, scan ScanSummary, pages []PageResult, opts ScanOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, scan.ID)
		s.mu.Unlock()
	}()

	s.publish(Event{Type: "start", ScanID: scan.ID, Message: "Re-running Lighthouse"})
	scan = s.runLighthouseAudits(ctx, scan, pages, opts)
	if ctx.Err() != nil {
		scan.Status = "cancelled"
		scan.Phase = "cancelled"
		scan.FinishedAt = time.Now()
		scan.Error = "scan cancelled"
		_ = s.store.UpdateScan(scan)
		s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Audit cancelled", Data: scan})
		return
	}
	scan.Status = "completed"
	scan.Phase = "completed"
	scan.FinishedAt = time.Now()
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Lighthouse complete", Data: scan})
}

func (s *Service) Subscribe(scanID string) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	s.mu.Lock()
	s.events[scanID] = append(s.events[scanID], ch)
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subscribers := s.events[scanID]
		for i, candidate := range subscribers {
			if candidate == ch {
				s.events[scanID] = append(subscribers[:i], subscribers[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

func (s *Service) runScan(ctx context.Context, scan ScanSummary, root *url.URL, opts ScanOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, scan.ID)
		s.mu.Unlock()
	}()

	s.publish(Event{Type: "start", ScanID: scan.ID, Message: "Scan started"})
	discoverer := Discoverer{Client: s.client}
	seeds, err := discoverer.Discover(ctx, root)
	if err != nil {
		s.publish(Event{Type: "warning", ScanID: scan.ID, Message: "Discovery failed; scanning the entered URL"})
		seeds = []string{root.String()}
	}
	if len(seeds) == 0 {
		seeds = []string{root.String()}
	}

	seen := map[string]bool{}
	analyzedURLs := map[string]bool{}
	skipped := 0
	var queue []string
	limit := 0
	if opts.CrawlLimit != nil {
		limit = *opts.CrawlLimit
	}
	enqueue := func(raw string) {
		if limit > 0 && len(seen)-skipped >= limit {
			return
		}
		normalized, ok := normalizePageURL(raw, root)
		if !ok || seen[normalized] || analyzedURLs[normalized] {
			return
		}
		parsed, err := url.Parse(normalized)
		if err != nil || !sameOrigin(parsed, root) {
			return
		}
		seen[normalized] = true
		queue = append(queue, normalized)
		scan.DiscoveredPages = len(seen) - skipped
		_ = s.store.UpdateScan(scan)
	}
	for _, seed := range seeds {
		enqueue(seed)
	}
	scan.Phase = "analyzing"
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "discovered", ScanID: scan.ID, Message: fmt.Sprintf("%d pages queued", len(queue)), Data: scan})

	jobs := make(chan string)
	results := make(chan PageResult)
	for i := 0; i < s.workers; i++ {
		go func() {
			for pageURL := range jobs {
				page := s.fetchAndAnalyzePage(ctx, pageURL, root)
				select {
				case results <- page:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	inFlight := 0
	analyzedPages := []PageResult{}
	for len(queue) > 0 || inFlight > 0 {
		if ctx.Err() != nil {
			s.cancelScan(scan, jobs)
			close(jobs)
			return
		}

		var next string
		var outbound chan<- string
		if len(queue) > 0 {
			next = queue[0]
			outbound = jobs
		}

		select {
		case outbound <- next:
			queue = queue[1:]
			inFlight++
			s.publish(Event{Type: "page-start", ScanID: scan.ID, PageURL: next})
		case page := <-results:
			inFlight--
			// Identity collapses pages that are really the same: redirect targets
			// (handled at fetch time) and query-string variants that share a
			// canonical URL (e.g. /mens and /mens?category=running).
			identity := pageIdentity(page, root)
			if identity != "" && analyzedURLs[identity] {
				skipped++
				scan.DiscoveredPages = len(seen) - skipped
				_ = s.store.UpdateScan(scan)
				s.publish(Event{Type: "page-skipped", ScanID: scan.ID, PageURL: page.URL, Message: "duplicate of an already-analyzed page"})
				continue
			}
			if identity != "" {
				analyzedURLs[identity] = true
				if page.URL != identity {
					page.URL = identity
					for i := range page.Links {
						page.Links[i].PageURL = identity
					}
				}
			}
			scan.CompletedPages++
			scan.FastCompletedPages = scan.CompletedPages
			if page.FetchError != "" {
				scan.FailedPages++
			}
			page.AuditStatus = "pending"
			page = NormalizePage(page)
			analyzedPages = append(analyzedPages, page)
			_ = s.store.SavePage(scan.ID, page)
			_ = s.store.UpdateScan(scan)
			s.publish(Event{Type: "page-analyzed", ScanID: scan.ID, PageURL: page.URL, Data: page})
			for _, link := range page.Links {
				if link.Kind == "internal" {
					enqueue(link.URL)
				}
			}
		case <-ctx.Done():
			s.cancelScan(scan, jobs)
			close(jobs)
			return
		}
	}

	close(jobs)
	scan.Phase = "fast-complete"
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "fast-complete", ScanID: scan.ID, Message: "Fast report ready", Data: scan})

	scan = s.runLighthouseAudits(ctx, scan, analyzedPages, opts)
	if ctx.Err() != nil {
		scan.Status = "cancelled"
		scan.Phase = "cancelled"
		scan.FinishedAt = time.Now()
		scan.Error = "scan cancelled"
		_ = s.store.UpdateScan(scan)
		s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Scan cancelled", Data: scan})
		return
	}
	scan.Status = "completed"
	scan.Phase = "completed"
	scan.FinishedAt = time.Now()
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Scan completed", Data: scan})
}

func (s *Service) cancelScan(scan ScanSummary, jobs chan string) {
	scan.Status = "cancelled"
	scan.Phase = "cancelled"
	scan.FinishedAt = time.Now()
	scan.Error = "scan cancelled"
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Scan cancelled", Data: scan})
}

func (s *Service) runLighthouseAudits(ctx context.Context, scan ScanSummary, pages []PageResult, opts ScanOptions) ScanSummary {
	auditPages := selectAuditPages(pages, opts, scan.RootURL)
	scan.AuditQueuedPages = len(auditPages)
	if scan.AuditQueuedPages == 0 {
		return scan
	}
	scan.Phase = "auditing"
	_ = s.store.UpdateScan(scan)

	rollup := newScoreRollup()
	for _, page := range auditPages {
		if ctx.Err() != nil {
			return scan
		}
		page.AuditStatus = "running"
		page.AuditError = ""
		_ = s.store.SavePage(scan.ID, page)
		s.publish(Event{Type: "audit-start", ScanID: scan.ID, PageURL: page.URL, Data: page})

		audited := s.auditPageWithLighthouse(ctx, page)
		if ctx.Err() != nil {
			return scan
		}
		if audited.AuditStatus == "failed" {
			scan.AuditFailedPages++
			s.publish(Event{Type: "audit-error", ScanID: scan.ID, PageURL: audited.URL, Data: audited})
		} else {
			scan.AuditCompletedPages++
			rollup.Add(audited.Lighthouse)
			scan.Scores = rollup.ScoreSet()
			s.publish(Event{Type: "audit-complete", ScanID: scan.ID, PageURL: audited.URL, Data: audited})
		}
		_ = s.store.SavePage(scan.ID, audited)
		_ = s.store.UpdateScan(scan)
	}
	return scan
}

func selectAuditPages(pages []PageResult, opts ScanOptions, rootURL string) []PageResult {
	if opts.LighthouseMode == "none" {
		return []PageResult{}
	}

	// Only pages that actually fetched can be audited by Lighthouse.
	candidates := make([]PageResult, 0, len(pages))
	for _, page := range pages {
		if page.FetchError == "" {
			candidates = append(candidates, page)
		}
	}

	// "all" mode audits every fetched page.
	if opts.LighthouseMode == "all" {
		return candidates
	}

	limit := opts.LighthouseLimit
	if limit <= 0 {
		limit = 5
	}
	if len(candidates) <= limit {
		return candidates
	}

	// "top" mode: always keep the home page, then fill the remaining slots with
	// the most content-heavy pages. The home page is pulled out before sorting —
	// pinning it inside the comparator would be unsafe because the comparator
	// sees shifting positions, not original indices.
	homeIndex := homePageIndex(candidates, rootURL)
	var home *PageResult
	rest := make([]PageResult, 0, len(candidates))
	for i := range candidates {
		if i == homeIndex {
			page := candidates[i]
			home = &page
			continue
		}
		rest = append(rest, candidates[i])
	}

	sort.SliceStable(rest, func(i, j int) bool {
		wi, wj := contentWeight(rest[i]), contentWeight(rest[j])
		if wi != wj {
			return wi > wj
		}
		return rest[i].URL < rest[j].URL
	})

	selected := make([]PageResult, 0, limit)
	if home != nil {
		selected = append(selected, *home)
	}
	for _, page := range rest {
		if len(selected) >= limit {
			break
		}
		selected = append(selected, page)
	}
	return selected
}

// contentWeight scores how content-heavy a page is. EDS blocks are the unit of
// real content, so they dominate; sections act as a fine tie-breaker. Links are
// deliberately excluded — utility pages like nav, footer and fragment shells are
// link-heavy but carry no content, and should not outrank actual pages.
func contentWeight(page PageResult) int {
	return page.BlockCount*100 + page.SectionCount
}

// homePageIndex finds the home page among candidates: an exact match against the
// crawl root if present, otherwise the page with the shallowest path. Returns -1
// when there are no candidates.
func homePageIndex(pages []PageResult, rootURL string) int {
	target := canonicalPathKey(rootURL)
	best := -1
	bestDepth := -1
	for i, page := range pages {
		if canonicalPathKey(page.URL) == target {
			return i
		}
		depth := strings.Count(strings.Trim(pagePath(page.URL), "/"), "/")
		if best == -1 || depth < bestDepth {
			best = i
			bestDepth = depth
		}
	}
	return best
}

// canonicalPathKey normalizes a URL for home-page comparison by lowercasing the
// host and dropping a trailing slash from the path.
func canonicalPathKey(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	path := strings.TrimRight(parsed.Path, "/")
	return strings.ToLower(parsed.Host) + path
}

func pagePath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return parsed.Path
}

func (s *Service) fetchAndAnalyzePage(ctx context.Context, pageURL string, root *url.URL) PageResult {
	page := PageResult{RequestedURL: pageURL, URL: pageURL}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		page.FetchError = err.Error()
		return page
	}
	req.Header.Set("User-Agent", "EDSAnalyser/0.1 (+https://localhost)")
	resp, err := s.client.Do(req)
	if err != nil {
		page.FetchError = err.Error()
		return page
	}
	defer resp.Body.Close()
	page.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		page.FetchError = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return page
	}

	// The HTTP client follows redirects, so resp.Request.URL is the URL that
	// actually served this content. Key the page on that final, same-origin URL
	// so several source URLs that redirect to the same page collapse into one.
	canonicalURL := finalPageURL(pageURL, resp, root)
	analyzed, err := AnalyzeHTML(canonicalURL, io.LimitReader(resp.Body, 16*1024*1024), root)
	if err != nil {
		page.FetchError = err.Error()
		return page
	}
	analyzed.StatusCode = resp.StatusCode
	analyzed.RequestedURL = pageURL
	for i := range analyzed.Links {
		analyzed.Links[i].PageURL = canonicalURL
	}
	analyzed.AuditStatus = "pending"
	return NormalizePage(analyzed)
}

// finalPageURL returns the URL that ultimately served the response after any
// redirects, normalized and constrained to the crawl origin. If the response
// ended up on a different origin (or cannot be normalized) the originally
// requested URL is kept so off-site redirects do not pollute the page list.
func finalPageURL(requested string, resp *http.Response, root *url.URL) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return requested
	}
	normalized, ok := normalizePageURL(resp.Request.URL.String(), root)
	if !ok {
		return requested
	}
	parsed, err := url.Parse(normalized)
	if err != nil || !sameOrigin(parsed, root) {
		return requested
	}
	return normalized
}

// pageIdentity returns the URL that should represent a page for deduplication.
// When a page declares a same-origin canonical URL that shares its path, the
// canonical is used so query-string variants (e.g. /mens?category=running)
// collapse onto the real page (/mens). The same-path guard is deliberate: it
// only merges query variants and never different paths, so a site that
// misconfigures every page's canonical to a single URL is not collapsed into
// one entry. Pages with no usable canonical keep their own URL.
func pageIdentity(page PageResult, root *url.URL) string {
	if strings.TrimSpace(page.Canonical) == "" {
		return page.URL
	}
	base, err := url.Parse(page.URL)
	if err != nil {
		base = root
	}
	canonical, ok := normalizePageURL(page.Canonical, base)
	if !ok {
		return page.URL
	}
	canonicalURL, err := url.Parse(canonical)
	if err != nil || !sameOrigin(canonicalURL, root) {
		return page.URL
	}
	pageURL, err := url.Parse(page.URL)
	if err != nil {
		return page.URL
	}
	if strings.TrimRight(canonicalURL.Path, "/") == strings.TrimRight(pageURL.Path, "/") {
		return canonical
	}
	return page.URL
}

func (s *Service) auditPageWithLighthouse(ctx context.Context, page PageResult) PageResult {
	scores, err := s.lighthouse.Audit(ctx, page.URL)
	if err != nil {
		page.AuditError = err.Error()
		page.AuditStatus = "failed"
	} else {
		page.Lighthouse = scores
		page.AuditStatus = "complete"
		page.AuditError = ""
	}
	return NormalizePage(page)
}

func (s *Service) publish(event Event) {
	event.Timestamp = time.Now()
	s.mu.Lock()
	subscribers := append([]chan Event(nil), s.events[event.ScanID]...)
	s.mu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

type scoreRollup struct {
	performance        float64
	performanceCount   int
	accessibility      float64
	accessibilityCount int
	bestPractices      float64
	bestPracticesCount int
	seo                float64
	seoCount           int
	health             float64
	healthCount        int
}

func newScoreRollup() *scoreRollup {
	return &scoreRollup{}
}

func (r *scoreRollup) Add(scores ScoreSet) {
	add := func(value *float64, sum *float64, count *int) {
		if value == nil {
			return
		}
		*sum += *value
		*count = *count + 1
	}
	add(scores.Performance, &r.performance, &r.performanceCount)
	add(scores.Accessibility, &r.accessibility, &r.accessibilityCount)
	add(scores.BestPractices, &r.bestPractices, &r.bestPracticesCount)
	add(scores.SEO, &r.seo, &r.seoCount)
	add(scores.Health, &r.health, &r.healthCount)
}

func (r *scoreRollup) ScoreSet() ScoreSet {
	return ScoreSet{
		Performance:   average(r.performance, r.performanceCount),
		Accessibility: average(r.accessibility, r.accessibilityCount),
		BestPractices: average(r.bestPractices, r.bestPracticesCount),
		SEO:           average(r.seo, r.seoCount),
		Health:        average(r.health, r.healthCount),
	}
}

func average(sum float64, count int) *float64 {
	if count == 0 {
		return nil
	}
	value := sum / float64(count)
	return &value
}

func HasLighthouseError(err string) bool {
	return strings.TrimSpace(err) != ""
}
