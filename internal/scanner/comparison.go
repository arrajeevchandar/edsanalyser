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
	CrawlLimit        *int
	CrawlMode         string
	RenderedDiscovery string
	LighthouseMode    string
	LighthouseLimit   int
}

type comparisonCrawlResult struct {
	Pages     []PageResult
	Discovery DiscoveryReport
}

type comparisonPageGroups struct {
	Matched             []ComparedPage
	UncertainMatches    []ComparedPage
	MissingInEDS        []PageResult
	ExtraInEDS          []PageResult
	SourceFetchFailures []PageResult
	EDSFetchFailures    []PageResult
}

func DefaultComparisonOptions() ComparisonOptions {
	return ComparisonOptions{CrawlMode: "exhaustive", RenderedDiscovery: "auto", LighthouseMode: "top", LighthouseLimit: 5}
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
	if opts.CrawlMode == "" {
		opts.CrawlMode = "exhaustive"
	}
	if opts.RenderedDiscovery == "" {
		opts.RenderedDiscovery = "auto"
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
	sourceCrawl := s.crawlForComparison(ctx, comparison.ID, "source", sourceRoot, opts)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}
	sourcePages := sourceCrawl.Pages
	comparison.SourceDiscovery = sourceCrawl.Discovery
	comparison.SourcePages = len(sourcePages)
	comparison.SourceFetchFailures = countFetchFailures(sourcePages)
	comparison.Phase = "eds-crawl"
	_ = s.store.UpdateComparison(comparison)

	edsCrawl := s.crawlForComparison(ctx, comparison.ID, "eds", edsRoot, opts)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}
	edsPages := edsCrawl.Pages
	comparison.EDSDiscovery = edsCrawl.Discovery
	comparison.EDSPages = len(edsPages)
	comparison.EDSFetchFailures = countFetchFailures(edsPages)
	comparison.Phase = "matching"
	_ = s.store.UpdateComparison(comparison)
	s.publish(Event{Type: "matching", ScanID: comparison.ID, Message: "Matching pages by path"})

	groups := buildComparisonPages(sourcePages, edsPages)
	comparison = summarizeFastComparison(comparison, groups)
	for _, page := range groups.Matched {
		_ = s.store.SaveComparedPage(comparison.ID, "matched", page)
	}
	for _, page := range groups.UncertainMatches {
		_ = s.store.SaveComparedPage(comparison.ID, "uncertainMatches", page)
	}
	for _, page := range groups.MissingInEDS {
		_ = s.store.SaveComparedPage(comparison.ID, "missingInEDS", ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, MatchType: "unmatched", MatchConfidence: "low", Source: page, Issues: []string{"Missing in migrated EDS site"}})
	}
	for _, page := range groups.ExtraInEDS {
		_ = s.store.SaveComparedPage(comparison.ID, "extraInEDS", ComparedPage{Path: comparisonPathKey(page.URL), Status: "review", Severity: 4, MatchType: "unmatched", MatchConfidence: "low", EDS: page, Issues: []string{"Extra page in migrated EDS site"}})
	}
	for _, page := range groups.SourceFetchFailures {
		_ = s.store.SaveComparedPage(comparison.ID, "sourceFetchFailures", ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, MatchType: "unmatched", MatchConfidence: "low", Source: page, Issues: []string{page.FetchError}})
	}
	for _, page := range groups.EDSFetchFailures {
		_ = s.store.SaveComparedPage(comparison.ID, "edsFetchFailures", ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, MatchType: "unmatched", MatchConfidence: "low", EDS: page, Issues: []string{page.FetchError}})
	}
	_ = s.store.UpdateComparison(comparison)
	s.publish(Event{Type: "fast-complete", ScanID: comparison.ID, Message: "Fast comparison ready", Data: comparison})

	auditTargets := append([]ComparedPage{}, groups.Matched...)
	auditTargets = append(auditTargets, groups.UncertainMatches...)
	auditTargets = s.auditComparison(ctx, comparison.ID, auditTargets, sourceRoot.String(), edsRoot.String(), opts, &comparison)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}
	auditTargets = s.visualCompare(ctx, comparison.ID, auditTargets, &comparison)
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

func (s *Service) crawlForComparison(ctx context.Context, comparisonID string, role string, root *url.URL, opts ComparisonOptions) comparisonCrawlResult {
	discoverer := Discoverer{Client: s.client}
	seeds, report := discoverer.DiscoverDetailed(ctx, root)
	if len(seeds) == 0 {
		seeds = []DiscoveredURL{{URL: root.String(), Source: "root"}}
		report.RootURL = root.String()
		report.Warnings = append(report.Warnings, "Discovery endpoints returned no pages; crawling the entered URL.")
	}
	s.publish(Event{Type: role + "-discovered", ScanID: comparisonID, Message: fmt.Sprintf("%s queued %d seed pages", role, len(seeds)), Data: report})

	seen := map[string]bool{}
	analyzedURLs := map[string]bool{}
	var queue []string
	limit := comparisonCrawlLimit(opts)
	enqueue := func(raw string, source string) bool {
		if limit > 0 && len(seen) >= limit {
			report.LimitHit = true
			if !hasWarning(report.Warnings, "Crawl limit hit before all discovered URLs were queued.") {
				report.Warnings = append(report.Warnings, "Crawl limit hit before all discovered URLs were queued.")
			}
			return false
		}
		normalized, ok := normalizePageURL(raw, root)
		if !ok {
			if looksLikeAssetOrDownload(raw, root) {
				report.SkippedAssets++
			}
			return false
		}
		parsed, err := url.Parse(normalized)
		if err != nil {
			return false
		}
		if !sameOrigin(parsed, root) {
			report.SkippedExternal++
			return false
		}
		if seen[normalized] || analyzedURLs[normalized] {
			report.Duplicates++
			return false
		}
		seen[normalized] = true
		queue = append(queue, normalized)
		report.TotalQueued = len(seen)
		switch source {
		case "static-link":
			report.FromStaticLinks++
		case "rendered-link":
			report.FromRenderedLinks++
		}
		return true
	}
	for _, seed := range seeds {
		enqueue(seed.URL, seed.Source)
	}
	if len(queue) == 1 && opts.RenderedDiscovery != "off" {
		if added := s.enqueueRenderedLinks(ctx, queue[0], root, enqueue, &report); added > 0 {
			report.Warnings = append(report.Warnings, "Rendered discovery was used because static discovery only found one page.")
		}
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
	renderedTried := map[string]bool{}
	for len(queue) > 0 || inFlight > 0 {
		if ctx.Err() != nil {
			close(jobs)
			return comparisonCrawlResult{Pages: pages, Discovery: NormalizeDiscoveryReport(report)}
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
				report.Duplicates++
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
			report.TotalAnalyzed = len(pages)
			s.publish(Event{Type: role + "-page-analyzed", ScanID: comparisonID, PageURL: page.URL, Data: page})
			staticAdded := 0
			for _, link := range page.Links {
				if link.Kind == "internal" && enqueue(link.URL, "static-link") {
					staticAdded++
				}
			}
			if opts.RenderedDiscovery != "off" && page.FetchError == "" && page.ScriptCount > 0 && staticAdded == 0 && !renderedTried[page.URL] {
				renderedTried[page.URL] = true
				added := s.enqueueRenderedLinks(ctx, page.URL, root, enqueue, &report)
				if added > 0 {
					report.Warnings = append(report.Warnings, fmt.Sprintf("Rendered discovery found %d links on %s.", added, page.URL))
				}
			}
		case <-ctx.Done():
			close(jobs)
			return comparisonCrawlResult{Pages: pages, Discovery: NormalizeDiscoveryReport(report)}
		}
	}
	close(jobs)
	sort.Slice(pages, func(i, j int) bool { return pages[i].URL < pages[j].URL })
	if len(pages) <= 1 {
		report.Warnings = append(report.Warnings, "Only one HTML page was discovered; sitemap, query-index, static links, and rendered links did not expose more pages.")
	}
	report.TotalQueued = len(seen)
	report.TotalAnalyzed = len(pages)
	return comparisonCrawlResult{Pages: pages, Discovery: NormalizeDiscoveryReport(report)}
}

func (s *Service) enqueueRenderedLinks(ctx context.Context, pageURL string, root *url.URL, enqueue func(string, string) bool, report *DiscoveryReport) int {
	if s.rendered == nil {
		return 0
	}
	links, err := s.rendered.Links(ctx, pageURL, root)
	if err != nil {
		message := err.Error()
		if !hasWarning(report.Warnings, message) {
			report.Warnings = append(report.Warnings, message)
		}
		return 0
	}
	added := 0
	for _, link := range links {
		if enqueue(link, "rendered-link") {
			added++
		}
	}
	return added
}

func comparisonCrawlLimit(opts ComparisonOptions) int {
	limit := 2000
	if opts.CrawlLimit != nil && *opts.CrawlLimit > 0 && *opts.CrawlLimit < limit {
		limit = *opts.CrawlLimit
	}
	return limit
}

func hasWarning(warnings []string, message string) bool {
	for _, warning := range warnings {
		if warning == message {
			return true
		}
	}
	return false
}

func buildComparisonPages(sourcePages []PageResult, edsPages []PageResult) comparisonPageGroups {
	sourceByPath := map[string]PageResult{}
	edsByPath := map[string]PageResult{}
	groups := comparisonPageGroups{}
	for _, page := range sourcePages {
		if page.FetchError != "" {
			groups.SourceFetchFailures = append(groups.SourceFetchFailures, page)
			continue
		}
		sourceByPath[comparisonPathKey(page.URL)] = page
	}
	for _, page := range edsPages {
		if page.FetchError != "" {
			groups.EDSFetchFailures = append(groups.EDSFetchFailures, page)
			continue
		}
		edsByPath[comparisonPathKey(page.URL)] = page
	}

	usedEDS := map[string]bool{}
	remainingSource := map[string]PageResult{}
	for _, key := range sortedStringKeys(sourceByPath) {
		source := sourceByPath[key]
		eds, ok := edsByPath[key]
		if ok {
			usedEDS[key] = true
			groups.Matched = append(groups.Matched, comparePagesWithMatch(key, source, eds, "exact", "high"))
			continue
		}
		remainingSource[key] = source
	}

	aliasMaps := buildAliasMaps(edsByPath, usedEDS)
	for _, sourceKey := range sortedStringKeys(remainingSource) {
		source := remainingSource[sourceKey]
		match, ok := findAliasMatch(source, aliasMaps, usedEDS)
		if !ok {
			groups.MissingInEDS = append(groups.MissingInEDS, source)
			continue
		}
		usedEDS[match.EDSKey] = true
		page := comparePagesWithMatch(match.DisplayKey, source, match.EDS, match.MatchType, match.Confidence)
		page.SourceAliases = sourceAliases(source)
		page.EDSAliases = edsAliases(match.EDS)
		if match.Confidence == "high" {
			groups.Matched = append(groups.Matched, page)
		} else {
			page.Status = "review"
			page.Issues = append(page.Issues, fmt.Sprintf("Matched by %s alias; verify this page pair.", match.MatchType))
			groups.UncertainMatches = append(groups.UncertainMatches, NormalizeComparedPage(page))
		}
	}
	for _, key := range sortedStringKeys(edsByPath) {
		if !usedEDS[key] {
			groups.ExtraInEDS = append(groups.ExtraInEDS, edsByPath[key])
		}
	}
	return groups
}

func comparePages(key string, source PageResult, eds PageResult) ComparedPage {
	return comparePagesWithMatch(key, source, eds, "exact", "high")
}

func comparePagesWithMatch(key string, source PageResult, eds PageResult, matchType string, confidence string) ComparedPage {
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
		Path:            key,
		Status:          status,
		Severity:        severity,
		MatchType:       matchType,
		MatchConfidence: confidence,
		SourceAliases:   sourceAliases(source),
		EDSAliases:      edsAliases(eds),
		Source:          source,
		EDS:             eds,
		FieldDiffs:      fieldDiffs,
		LinkDiffs:       linkDiffs,
		Issues:          issues,
	})
}

type aliasCandidate struct {
	Key        string
	Type       string
	Confidence string
}

type aliasMatch struct {
	DisplayKey string
	EDSKey     string
	EDS        PageResult
	MatchType  string
	Confidence string
}

func buildAliasMaps(pages map[string]PageResult, used map[string]bool) map[string]map[string]PageResult {
	result := map[string]map[string]PageResult{
		"exact":     {},
		"redirect":  {},
		"canonical": {},
		"og":        {},
	}
	for key, page := range pages {
		if used[key] {
			continue
		}
		for _, alias := range pageAliases(page) {
			if alias.Key == "" {
				continue
			}
			if _, exists := result[alias.Type][alias.Key]; !exists {
				result[alias.Type][alias.Key] = page
			}
		}
	}
	return result
}

func findAliasMatch(source PageResult, edsAliases map[string]map[string]PageResult, usedEDS map[string]bool) (aliasMatch, bool) {
	edsPriority := []string{"exact", "redirect", "canonical", "og"}
	for _, sourceAlias := range pageAliases(source) {
		for _, edsType := range edsPriority {
			eds, ok := edsAliases[edsType][sourceAlias.Key]
			if !ok {
				continue
			}
			edsKey := comparisonPathKey(eds.URL)
			if usedEDS[edsKey] {
				continue
			}
			matchType, confidence := combineAliasMatch(sourceAlias, edsType)
			return aliasMatch{
				DisplayKey: sourceAlias.Key,
				EDSKey:     edsKey,
				EDS:        eds,
				MatchType:  matchType,
				Confidence: confidence,
			}, true
		}
	}
	return aliasMatch{}, false
}

func combineAliasMatch(sourceAlias aliasCandidate, edsType string) (string, string) {
	matchType := sourceAlias.Type
	confidence := sourceAlias.Confidence
	if aliasRank(edsType) > aliasRank(matchType) {
		matchType = edsType
	}
	if aliasConfidenceRank(aliasConfidence(edsType)) < aliasConfidenceRank(confidence) {
		confidence = aliasConfidence(edsType)
	}
	return matchType, confidence
}

func pageAliases(page PageResult) []aliasCandidate {
	candidates := []aliasCandidate{{Key: comparisonPathKey(page.URL), Type: "exact", Confidence: "high"}}
	if key := comparisonPathKey(page.RequestedURL); key != "" && key != candidates[0].Key {
		candidates = append(candidates, aliasCandidate{Key: key, Type: "redirect", Confidence: "high"})
	}
	if key := comparisonPathKey(page.Canonical); key != "" && key != "/" {
		candidates = append(candidates, aliasCandidate{Key: key, Type: "canonical", Confidence: "medium"})
	}
	if key := comparisonPathKey(page.OG.URL); key != "" && key != "/" {
		candidates = append(candidates, aliasCandidate{Key: key, Type: "og", Confidence: "medium"})
	}
	return dedupeAliases(candidates)
}

func sourceAliases(page PageResult) []string {
	return aliasStrings(pageAliases(page))
}

func edsAliases(page PageResult) []string {
	return aliasStrings(pageAliases(page))
}

func aliasStrings(aliases []aliasCandidate) []string {
	values := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		values = append(values, alias.Type+":"+alias.Key)
	}
	sort.Strings(values)
	return values
}

func dedupeAliases(aliases []aliasCandidate) []aliasCandidate {
	seen := map[string]bool{}
	var result []aliasCandidate
	for _, alias := range aliases {
		if alias.Key == "" || seen[alias.Type+":"+alias.Key] {
			continue
		}
		seen[alias.Type+":"+alias.Key] = true
		result = append(result, alias)
	}
	return result
}

func aliasRank(value string) int {
	switch value {
	case "exact":
		return 0
	case "redirect":
		return 1
	case "canonical":
		return 2
	case "og":
		return 3
	default:
		return 4
	}
}

func aliasConfidence(value string) string {
	if value == "exact" || value == "redirect" {
		return "high"
	}
	return "medium"
}

func aliasConfidenceRank(value string) int {
	switch value {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
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
	sourceSelected := pagesByURL(selectedSource)
	edsSelected := pagesByURL(selectedEDS)
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
		if _, ok := sourceSelected[matched[i].Source.URL]; ok {
			page := s.auditPageWithLighthouse(ctx, matched[i].Source)
			matched[i].Source = page
			if page.AuditStatus == "failed" {
				summary.LighthouseFailed++
			} else {
				summary.LighthouseCompleted++
			}
			s.publish(Event{Type: "comparison-audit-complete", ScanID: comparisonID, PageURL: page.URL, Data: page})
		}
		if _, ok := edsSelected[matched[i].EDS.URL]; ok {
			page := s.auditPageWithLighthouse(ctx, matched[i].EDS)
			matched[i].EDS = page
			if page.AuditStatus == "failed" {
				summary.LighthouseFailed++
			} else {
				summary.LighthouseCompleted++
			}
			s.publish(Event{Type: "comparison-audit-complete", ScanID: comparisonID, PageURL: page.URL, Data: page})
		}
		_ = s.store.SaveComparedPage(comparisonID, comparedPageGroup(matched[i]), matched[i])
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
			_ = s.store.SaveComparedPage(comparisonID, comparedPageGroup(matched[i]), matched[i])
			_ = s.store.UpdateComparison(*summary)
			s.publish(Event{Type: "visual-complete", ScanID: comparisonID, PageURL: matched[i].EDS.URL, Data: visual})
		}
	}
	return matched
}

func comparedPageGroup(page ComparedPage) string {
	if page.MatchConfidence == "medium" || page.MatchType == "canonical" || page.MatchType == "og" {
		return "uncertainMatches"
	}
	return "matched"
}

func summarizeFastComparison(summary ComparisonSummary, groups comparisonPageGroups) ComparisonSummary {
	summary.Phase = "fast-complete"
	summary.MatchedPages = len(groups.Matched)
	summary.UncertainMatches = len(groups.UncertainMatches)
	summary.MissingInEDS = len(groups.MissingInEDS)
	summary.ExtraInEDS = len(groups.ExtraInEDS)
	summary.SourceFetchFailures = len(groups.SourceFetchFailures)
	summary.EDSFetchFailures = len(groups.EDSFetchFailures)
	summary.MetadataDiffs = 0
	summary.LinkDiffs = 0
	for _, page := range append(append([]ComparedPage{}, groups.Matched...), groups.UncertainMatches...) {
		summary.MetadataDiffs += len(page.FieldDiffs)
		summary.LinkDiffs += len(page.LinkDiffs)
	}
	summary.MigrationScore = migrationScore(summary)
	return summary
}

func migrationScore(summary ComparisonSummary) *float64 {
	total := summary.MatchedPages + summary.UncertainMatches + summary.MissingInEDS + summary.ExtraInEDS
	if total == 0 {
		return nil
	}
	score := 100.0
	score -= float64(summary.UncertainMatches) * 2
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
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
	lower := strings.ToLower(path)
	if lower == "/index" || lower == "/index.html" {
		return "/"
	}
	if strings.HasSuffix(lower, "/index") {
		path = path[:len(path)-len("/index")]
	} else if strings.HasSuffix(lower, "/index.html") {
		path = path[:len(path)-len("/index.html")]
	}
	if path == "" {
		path = "/"
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

func pagesByURL(pages []PageResult) map[string]PageResult {
	result := map[string]PageResult{}
	for _, page := range pages {
		result[page.URL] = page
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
