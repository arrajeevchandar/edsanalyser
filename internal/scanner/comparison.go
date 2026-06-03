package scanner

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type ComparisonOptions struct {
	CrawlLimit      *int
	LighthouseMode  string
	LighthouseLimit int
}

func DefaultComparisonOptions() ComparisonOptions {
	return ComparisonOptions{LighthouseMode: "top", LighthouseLimit: 5}
}

func (s *Service) StartComparison(parent context.Context, sourceInput string, edsInput string, opts ComparisonOptions) (ComparisonSummary, error) {
	sourceRoot, err := NormalizeInputURL(sourceInput)
	if err != nil {
		return ComparisonSummary{}, err
	}
	edsRoot, err := NormalizeInputURL(edsInput)
	if err != nil {
		return ComparisonSummary{}, err
	}
	isEDS, _, err := s.CheckEDS(parent, edsInput)
	if err != nil {
		return ComparisonSummary{}, err
	}
	if !isEDS {
		return ComparisonSummary{}, fmt.Errorf("migrated EDS URL must be an EDS site")
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
	comparison := ComparisonSummary{
		ID:             id,
		SourceInputURL: sourceInput,
		EDSInputURL:    edsInput,
		SourceRootURL:  sourceRoot.String(),
		EDSRootURL:     edsRoot.String(),
		Status:         "running",
		Phase:          "source-crawl",
		StartedAt:      time.Now(),
	}
	if err := s.store.CreateComparison(comparison); err != nil {
		cancel()
		return ComparisonSummary{}, err
	}

	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()

	go s.runComparison(ctx, comparison, sourceRoot, edsRoot, opts)
	return comparison, parent.Err()
}

func (s *Service) ListComparisons() ([]ComparisonSummary, error) {
	return s.store.ListComparisons()
}

func (s *Service) GetComparison(id string) (ComparisonResult, error) {
	return s.store.GetComparison(id)
}

func (s *Service) CancelComparison(id string) error {
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel == nil {
		return fmt.Errorf("comparison is not running")
	}
	cancel()
	s.publish(Event{Type: "cancel", ScanID: id, Message: "Comparison cancellation requested"})
	return nil
}

func (s *Service) runComparison(ctx context.Context, comparison ComparisonSummary, sourceRoot *url.URL, edsRoot *url.URL, opts ComparisonOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, comparison.ID)
		s.mu.Unlock()
	}()

	s.publish(Event{Type: "start", ScanID: comparison.ID, Message: "Comparison started"})
	sourcePages := s.crawlForComparison(ctx, comparison.ID, "source", sourceRoot, opts)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}
	comparison.SourcePages = len(sourcePages)
	comparison.SourceFetchFailures = countFetchFailures(sourcePages)
	comparison.Phase = "eds-crawl"
	_ = s.store.UpdateComparison(comparison)

	edsPages := s.crawlForComparison(ctx, comparison.ID, "eds", edsRoot, opts)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}
	comparison.EDSPages = len(edsPages)
	comparison.EDSFetchFailures = countFetchFailures(edsPages)
	comparison.Phase = "matching"
	_ = s.store.UpdateComparison(comparison)
	s.publish(Event{Type: "matching", ScanID: comparison.ID, Message: "Matching pages by path"})

	matched, missing, extra, sourceFailures, edsFailures := buildComparisonPages(sourcePages, edsPages)
	comparison = summarizeFastComparison(comparison, matched, missing, extra, sourceFailures, edsFailures)
	for _, page := range matched {
		_ = s.store.SaveComparedPage(comparison.ID, "matched", page)
	}
	for _, page := range missing {
		_ = s.store.SaveComparedPage(comparison.ID, "missingInEDS", ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, Source: page, Issues: []string{"Missing in migrated EDS site"}})
	}
	for _, page := range extra {
		_ = s.store.SaveComparedPage(comparison.ID, "extraInEDS", ComparedPage{Path: comparisonPathKey(page.URL), Status: "review", Severity: 4, EDS: page, Issues: []string{"Extra page in migrated EDS site"}})
	}
	for _, page := range sourceFailures {
		_ = s.store.SaveComparedPage(comparison.ID, "sourceFetchFailures", ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, Source: page, Issues: []string{page.FetchError}})
	}
	for _, page := range edsFailures {
		_ = s.store.SaveComparedPage(comparison.ID, "edsFetchFailures", ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, EDS: page, Issues: []string{page.FetchError}})
	}
	_ = s.store.UpdateComparison(comparison)
	s.publish(Event{Type: "fast-complete", ScanID: comparison.ID, Message: "Fast comparison ready", Data: comparison})

	matched = s.auditComparison(ctx, comparison.ID, matched, sourceRoot.String(), edsRoot.String(), opts, &comparison)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}
	matched = s.visualCompare(ctx, comparison.ID, matched, &comparison)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}

	comparison.Status = "completed"
	comparison.Phase = "completed"
	comparison.FinishedAt = time.Now()
	comparison.MigrationScore = migrationScore(comparison)
	_ = s.store.UpdateComparison(comparison)
	s.publish(Event{Type: "complete", ScanID: comparison.ID, Message: "Comparison completed", Data: comparison})
}

func (s *Service) cancelComparison(comparison ComparisonSummary) {
	comparison.Status = "cancelled"
	comparison.Phase = "cancelled"
	comparison.FinishedAt = time.Now()
	comparison.Error = "comparison cancelled"
	_ = s.store.UpdateComparison(comparison)
	s.publish(Event{Type: "complete", ScanID: comparison.ID, Message: "Comparison cancelled", Data: comparison})
}

func (s *Service) crawlForComparison(ctx context.Context, comparisonID string, role string, root *url.URL, opts ComparisonOptions) []PageResult {
	discoverer := Discoverer{Client: s.client}
	seeds, err := discoverer.Discover(ctx, root)
	if err != nil || len(seeds) == 0 {
		seeds = []string{root.String()}
	}
	s.publish(Event{Type: role + "-discovered", ScanID: comparisonID, Message: fmt.Sprintf("%s queued %d seed pages", role, len(seeds))})

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
	}
	for _, seed := range seeds {
		enqueue(seed)
	}

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
	pages := []PageResult{}
	for len(queue) > 0 || inFlight > 0 {
		if ctx.Err() != nil {
			close(jobs)
			return pages
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
			s.publish(Event{Type: role + "-page-start", ScanID: comparisonID, PageURL: next})
		case page := <-results:
			inFlight--
			identity := pageIdentity(page, root)
			if identity != "" && analyzedURLs[identity] {
				skipped++
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
			page.AuditStatus = "pending"
			page = NormalizePage(page)
			pages = append(pages, page)
			s.publish(Event{Type: role + "-page-analyzed", ScanID: comparisonID, PageURL: page.URL, Data: page})
			for _, link := range page.Links {
				if link.Kind == "internal" {
					enqueue(link.URL)
				}
			}
		case <-ctx.Done():
			close(jobs)
			return pages
		}
	}
	close(jobs)
	sort.Slice(pages, func(i, j int) bool { return pages[i].URL < pages[j].URL })
	return pages
}

func buildComparisonPages(sourcePages []PageResult, edsPages []PageResult) ([]ComparedPage, []PageResult, []PageResult, []PageResult, []PageResult) {
	sourceByPath := map[string]PageResult{}
	edsByPath := map[string]PageResult{}
	var sourceFailures []PageResult
	var edsFailures []PageResult
	for _, page := range sourcePages {
		if page.FetchError != "" {
			sourceFailures = append(sourceFailures, page)
			continue
		}
		sourceByPath[comparisonPathKey(page.URL)] = page
	}
	for _, page := range edsPages {
		if page.FetchError != "" {
			edsFailures = append(edsFailures, page)
			continue
		}
		edsByPath[comparisonPathKey(page.URL)] = page
	}

	keys := sortedStringKeys(sourceByPath)
	var matched []ComparedPage
	var missing []PageResult
	for _, key := range keys {
		source := sourceByPath[key]
		eds, ok := edsByPath[key]
		if !ok {
			missing = append(missing, source)
			continue
		}
		delete(edsByPath, key)
		matched = append(matched, comparePages(key, source, eds))
	}
	var extra []PageResult
	for _, key := range sortedStringKeys(edsByPath) {
		extra = append(extra, edsByPath[key])
	}
	return matched, missing, extra, sourceFailures, edsFailures
}

func comparePages(key string, source PageResult, eds PageResult) ComparedPage {
	fieldDiffs := metadataDiffs(source, eds)
	linkDiffs := linkDiffs(source, eds)
	issues := []string{}
	severity := len(fieldDiffs) + len(linkDiffs)
	if source.StatusCode != eds.StatusCode {
		issues = append(issues, fmt.Sprintf("Status code changed from %d to %d", source.StatusCode, eds.StatusCode))
		severity += 2
	}
	if eds.BlockCount == 0 {
		issues = append(issues, "EDS page has no detected blocks")
		severity += 3
	}
	status := "pass"
	if severity > 0 {
		status = "review"
	}
	if severity >= 6 {
		status = "fail"
	}
	return NormalizeComparedPage(ComparedPage{
		Path:       key,
		Status:     status,
		Severity:   severity,
		Source:     source,
		EDS:        eds,
		FieldDiffs: fieldDiffs,
		LinkDiffs:  linkDiffs,
		Issues:     issues,
	})
}

func metadataDiffs(source PageResult, eds PageResult) []FieldDiff {
	checks := []struct {
		field  string
		a      string
		b      string
		urlish bool
	}{
		{"title", source.Title, eds.Title, false},
		{"h1", source.H1, eds.H1, false},
		{"description", source.Description, eds.Description, false},
		{"canonical", source.Canonical, eds.Canonical, true},
		{"robots", source.Robots, eds.Robots, false},
		{"lang", source.Lang, eds.Lang, false},
		{"og:title", source.OG.Title, eds.OG.Title, false},
		{"og:description", source.OG.Description, eds.OG.Description, false},
		{"og:image", source.OG.Image, eds.OG.Image, true},
		{"og:url", source.OG.URL, eds.OG.URL, true},
		{"og:type", source.OG.Type, eds.OG.Type, false},
		{"og:site_name", source.OG.SiteName, eds.OG.SiteName, false},
	}
	var diffs []FieldDiff
	for _, check := range checks {
		a := comparableValue(check.a, check.urlish)
		b := comparableValue(check.b, check.urlish)
		if a != b {
			diffs = append(diffs, FieldDiff{Field: check.field, Source: strings.TrimSpace(check.a), EDS: strings.TrimSpace(check.b), Status: "review"})
		}
	}
	return diffs
}

func linkDiffs(source PageResult, eds PageResult) []FieldDiff {
	var diffs []FieldDiff
	for _, kind := range []string{"internal", "external", "asset"} {
		sourceSet := linkSet(source.Links, kind)
		edsSet := linkSet(eds.Links, kind)
		missing := setDiff(sourceSet, edsSet)
		added := setDiff(edsSet, sourceSet)
		if len(missing) > 0 {
			diffs = append(diffs, FieldDiff{Field: "missing " + kindName(kind), Source: strings.Join(limitStrings(missing, 20), ", "), Status: "review"})
		}
		if len(added) > 0 {
			diffs = append(diffs, FieldDiff{Field: "added " + kindName(kind), EDS: strings.Join(limitStrings(added, 20), ", "), Status: "review"})
		}
	}
	return diffs
}

func (s *Service) auditComparison(ctx context.Context, comparisonID string, matched []ComparedPage, sourceRoot string, edsRoot string, opts ComparisonOptions, summary *ComparisonSummary) []ComparedPage {
	scanOpts := ScanOptions{LighthouseMode: opts.LighthouseMode, LighthouseLimit: opts.LighthouseLimit}
	var sourcePages []PageResult
	var edsPages []PageResult
	for _, page := range matched {
		sourcePages = append(sourcePages, page.Source)
		edsPages = append(edsPages, page.EDS)
	}
	selectedSource := selectAuditPages(sourcePages, scanOpts, sourceRoot)
	selectedEDS := selectAuditPages(edsPages, scanOpts, edsRoot)
	sourceSelected := pagesByPath(selectedSource)
	edsSelected := pagesByPath(selectedEDS)
	summary.LighthouseQueued = len(sourceSelected) + len(edsSelected)
	if summary.LighthouseQueued == 0 {
		return matched
	}
	summary.Phase = "lighthouse"
	_ = s.store.UpdateComparison(*summary)

	for i := range matched {
		if ctx.Err() != nil {
			return matched
		}
		if _, ok := sourceSelected[matched[i].Path]; ok {
			page := s.auditPageWithLighthouse(ctx, matched[i].Source)
			matched[i].Source = page
			if page.AuditStatus == "failed" {
				summary.LighthouseFailed++
			} else {
				summary.LighthouseCompleted++
			}
			s.publish(Event{Type: "comparison-audit-complete", ScanID: comparisonID, PageURL: page.URL, Data: page})
		}
		if _, ok := edsSelected[matched[i].Path]; ok {
			page := s.auditPageWithLighthouse(ctx, matched[i].EDS)
			matched[i].EDS = page
			if page.AuditStatus == "failed" {
				summary.LighthouseFailed++
			} else {
				summary.LighthouseCompleted++
			}
			s.publish(Event{Type: "comparison-audit-complete", ScanID: comparisonID, PageURL: page.URL, Data: page})
		}
		_ = s.store.SaveComparedPage(comparisonID, "matched", matched[i])
		_ = s.store.UpdateComparison(*summary)
	}
	return matched
}

func (s *Service) visualCompare(ctx context.Context, comparisonID string, matched []ComparedPage, summary *ComparisonSummary) []ComparedPage {
	summary.Phase = "visual-diff"
	summary.VisualQueued = len(matched) * len(DefaultVisualViewports)
	_ = s.store.UpdateComparison(*summary)
	for i := range matched {
		for _, viewport := range DefaultVisualViewports {
			if ctx.Err() != nil {
				return matched
			}
			visual := s.visual.Diff(ctx, comparisonID, matched[i].Path, matched[i].Source.URL, matched[i].EDS.URL, viewport)
			matched[i].Visuals = append(matched[i].Visuals, visual)
			if visual.Status == "failed" {
				summary.VisualFailed++
			} else {
				summary.VisualCompleted++
			}
			switch visual.Status {
			case "review":
				summary.VisualReview++
				matched[i].Severity += 2
			case "fail":
				summary.VisualFail++
				matched[i].Severity += 4
			case "failed":
				matched[i].Severity++
			}
			matched[i].Status = pageStatusFromSeverity(matched[i].Severity)
			_ = s.store.SaveComparisonVisual(comparisonID, matched[i].Path, visual)
			_ = s.store.SaveComparedPage(comparisonID, "matched", matched[i])
			_ = s.store.UpdateComparison(*summary)
			s.publish(Event{Type: "visual-complete", ScanID: comparisonID, PageURL: matched[i].EDS.URL, Data: visual})
		}
	}
	return matched
}

func summarizeFastComparison(summary ComparisonSummary, matched []ComparedPage, missing []PageResult, extra []PageResult, sourceFailures []PageResult, edsFailures []PageResult) ComparisonSummary {
	summary.Phase = "fast-complete"
	summary.MatchedPages = len(matched)
	summary.MissingInEDS = len(missing)
	summary.ExtraInEDS = len(extra)
	summary.SourceFetchFailures = len(sourceFailures)
	summary.EDSFetchFailures = len(edsFailures)
	summary.MetadataDiffs = 0
	summary.LinkDiffs = 0
	for _, page := range matched {
		summary.MetadataDiffs += len(page.FieldDiffs)
		summary.LinkDiffs += len(page.LinkDiffs)
	}
	summary.MigrationScore = migrationScore(summary)
	return summary
}

func migrationScore(summary ComparisonSummary) *float64 {
	total := summary.MatchedPages + summary.MissingInEDS + summary.ExtraInEDS
	if total == 0 {
		return nil
	}
	score := 100.0
	score -= float64(summary.MissingInEDS) * 8
	score -= float64(summary.ExtraInEDS) * 3
	score -= float64(summary.MetadataDiffs) * 0.8
	score -= float64(summary.LinkDiffs) * 0.6
	score -= float64(summary.VisualReview) * 2
	score -= float64(summary.VisualFail) * 6
	score -= float64(summary.SourceFetchFailures+summary.EDSFetchFailures) * 4
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return &score
}

func comparisonPathKey(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		value := strings.Trim(strings.ToLower(strings.TrimSpace(raw)), "/")
		if value == "" {
			return "/"
		}
		return "/" + value
	}
	path := strings.TrimSpace(parsed.Path)
	if path == "" || path == "/" {
		return "/"
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.ToLower(path)
}

func comparableValue(value string, urlish bool) string {
	value = strings.TrimSpace(value)
	if !urlish {
		return strings.Join(strings.Fields(strings.ToLower(value)), " ")
	}
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Path == "" {
		return strings.ToLower(value)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	return strings.ToLower(path)
}

func linkSet(links []LinkInfo, kind string) map[string]bool {
	set := map[string]bool{}
	for _, link := range links {
		if link.Kind != kind {
			continue
		}
		switch kind {
		case "internal", "asset":
			set[comparableValue(link.URL, true)] = true
		default:
			set[strings.ToLower(strings.TrimSpace(link.URL))] = true
		}
	}
	return set
}

func setDiff(a map[string]bool, b map[string]bool) []string {
	var values []string
	for value := range a {
		if !b[value] {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func kindName(kind string) string {
	if kind == "asset" {
		return "assets"
	}
	return kind + " links"
}

func sortedStringKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pagesByPath(pages []PageResult) map[string]PageResult {
	result := map[string]PageResult{}
	for _, page := range pages {
		result[comparisonPathKey(page.URL)] = page
	}
	return result
}

func countFetchFailures(pages []PageResult) int {
	count := 0
	for _, page := range pages {
		if page.FetchError != "" {
			count++
		}
	}
	return count
}

func pageStatusFromSeverity(severity int) string {
	if severity >= 6 {
		return "fail"
	}
	if severity > 0 {
		return "review"
	}
	return "pass"
}
