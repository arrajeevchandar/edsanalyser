package scanner

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
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
	visualCancel := s.cancels[id+"-visual"]
	lighthouseCancel := s.cancels[id+"-lighthouse"]
	s.mu.Unlock()
	if cancel == nil && visualCancel == nil && lighthouseCancel == nil {
		return fmt.Errorf("comparison is not running")
	}
	if cancel != nil {
		cancel()
	}
	if visualCancel != nil {
		visualCancel()
	}
	if lighthouseCancel != nil {
		lighthouseCancel()
	}
	s.publish(Event{Type: "cancel", ScanID: id, Message: "Comparison cancellation requested"})
	return nil
}

func (s *Service) UpdateComparisonMatch(id string, override MatchOverride) (ComparisonResult, error) {
	if strings.TrimSpace(override.SourceURL) == "" || strings.TrimSpace(override.EDSURL) == "" {
		return ComparisonResult{}, fmt.Errorf("sourceUrl and edsUrl are required")
	}
	if err := s.store.SaveComparisonMatchOverride(id, override); err != nil {
		return ComparisonResult{}, err
	}
	sourcePages, err := s.store.ListComparisonRolePages(id, "source")
	if err != nil {
		return ComparisonResult{}, err
	}
	edsPages, err := s.store.ListComparisonRolePages(id, "eds")
	if err != nil {
		return ComparisonResult{}, err
	}
	if len(sourcePages) == 0 && len(edsPages) == 0 {
		return ComparisonResult{}, fmt.Errorf("comparison pages are not available for rematching")
	}
	overrides, err := s.store.ListComparisonMatchOverrides(id)
	if err != nil {
		return ComparisonResult{}, err
	}
	result, err := s.store.GetComparison(id)
	if err != nil {
		return ComparisonResult{}, err
	}
	groups := buildComparisonPagesWithOverrides(sourcePages, edsPages, overrides)
	summary := summarizeFastComparison(result.Summary, groups)
	summary.MatchesUpdatedAt = time.Now()
	summary.FastReady = true
	if err := s.store.ReplaceComparedPages(id, groups); err != nil {
		return ComparisonResult{}, err
	}
	if err := s.store.UpdateComparison(summary); err != nil {
		return ComparisonResult{}, err
	}
	s.publish(Event{Type: "matches-updated", ScanID: id, Message: "Manual match updated", Data: summary})
	return s.store.GetComparison(id)
}

func (s *Service) RunComparisonVisuals(parent context.Context, id string, pageKeys []string) (ComparisonSummary, error) {
	result, err := s.store.GetComparison(id)
	if err != nil {
		return ComparisonSummary{}, err
	}
	targets := comparisonTargetsByKey(result, pageKeys)
	summary := result.Summary
	if len(targets) == 0 {
		return summary, nil
	}
	summary.BackgroundPhase = "visual-diff"
	summary.Phase = "visual-diff"
	summary.VisualQueued = len(targets) * len(DefaultVisualViewports)
	summary.VisualCompleted = 0
	summary.VisualFailed = 0
	summary.VisualReview = 0
	summary.VisualFail = 0
	_ = s.store.UpdateComparison(summary)
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id+"-visual"] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.cancels, id+"-visual")
			s.mu.Unlock()
		}()
		updated := summary
		_ = s.visualCompare(ctx, id, targets, &updated)
		updated.BackgroundPhase = ""
		if updated.Status != "running" {
			updated.Phase = updated.Status
		}
		_ = s.store.UpdateComparison(updated)
		s.publish(Event{Type: "visual-run-complete", ScanID: id, Message: "Visual comparison complete", Data: updated})
		cancel()
	}()
	return summary, parent.Err()
}

// RunComparisonLighthouse audits selected matched pages on demand. sides
// controls which columns are refreshed ("source"/"legacy", "eds", or empty for
// both). It runs in the background and streams progress, so it works even after
// the comparison itself has completed.
func (s *Service) RunComparisonLighthouse(parent context.Context, id string, pageKeys []string, sides []string) (ComparisonSummary, error) {
	result, err := s.store.GetComparison(id)
	if err != nil {
		return ComparisonSummary{}, err
	}
	targets := comparisonTargetsByKey(result, pageKeys)
	summary := result.Summary
	if len(targets) == 0 {
		return summary, nil
	}
	doSource, doEDS := lighthouseSides(sides)
	queued := 0
	for _, page := range targets {
		if doSource && page.Source.URL != "" && page.Source.FetchError == "" {
			queued++
		}
		if doEDS && page.EDS.URL != "" && page.EDS.FetchError == "" {
			queued++
		}
	}
	if queued == 0 {
		return summary, nil
	}

	summary.BackgroundPhase = "lighthouse"
	summary.Phase = "lighthouse"
	summary.LighthouseQueued = queued
	summary.LighthouseCompleted = 0
	summary.LighthouseFailed = 0
	_ = s.store.UpdateComparison(summary)

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id+"-lighthouse"] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.cancels, id+"-lighthouse")
			s.mu.Unlock()
		}()
		updated := summary
		for i := range targets {
			if ctx.Err() != nil {
				break
			}
			page := targets[i]
			if doSource && page.Source.URL != "" && page.Source.FetchError == "" {
				audited := s.auditPageWithLighthouse(ctx, page.Source)
				page.Source = audited
				if audited.AuditStatus == "failed" {
					updated.LighthouseFailed++
				} else {
					updated.LighthouseCompleted++
				}
				s.publish(Event{Type: "comparison-audit-complete", ScanID: id, PageURL: audited.URL, Data: audited})
			}
			if ctx.Err() != nil {
				break
			}
			if doEDS && page.EDS.URL != "" && page.EDS.FetchError == "" {
				audited := s.auditPageWithLighthouse(ctx, page.EDS)
				page.EDS = audited
				if audited.AuditStatus == "failed" {
					updated.LighthouseFailed++
				} else {
					updated.LighthouseCompleted++
				}
				s.publish(Event{Type: "comparison-audit-complete", ScanID: id, PageURL: audited.URL, Data: audited})
			}
			_ = s.store.SaveComparedPage(id, comparedPageGroup(page), page)
			_ = s.store.UpdateComparison(updated)
		}
		updated.BackgroundPhase = ""
		if updated.Status != "running" {
			updated.Phase = updated.Status
		}
		_ = s.store.UpdateComparison(updated)
		s.publish(Event{Type: "lighthouse-run-complete", ScanID: id, Message: "Lighthouse run complete", Data: updated})
		cancel()
	}()
	return summary, parent.Err()
}

func lighthouseSides(sides []string) (bool, bool) {
	if len(sides) == 0 {
		return true, true
	}
	doSource, doEDS := false, false
	for _, side := range sides {
		switch strings.ToLower(strings.TrimSpace(side)) {
		case "source", "legacy":
			doSource = true
		case "eds":
			doEDS = true
		case "both":
			doSource, doEDS = true, true
		}
	}
	return doSource, doEDS
}

func (s *Service) runComparison(ctx context.Context, comparison ComparisonSummary, sourceRoot *url.URL, edsRoot *url.URL, opts ComparisonOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, comparison.ID)
		s.mu.Unlock()
	}()

	s.publish(Event{Type: "start", ScanID: comparison.ID, Message: "Comparison started"})
	comparison.Phase = "crawling"
	comparison.BackgroundPhase = "fast"
	_ = s.store.UpdateComparison(comparison)

	var mu sync.Mutex
	sourcePages := map[string]PageResult{}
	edsPages := map[string]PageResult{}
	sourceDiscovery := NormalizeDiscoveryReport(DiscoveryReport{RootURL: sourceRoot.String()})
	edsDiscovery := NormalizeDiscoveryReport(DiscoveryReport{RootURL: edsRoot.String()})
	overrides, _ := s.store.ListComparisonMatchOverrides(comparison.ID)
	var latestGroups comparisonPageGroups

	recomputeLocked := func(phase string) {
		if phase != "" {
			comparison.Phase = phase
		}
		comparison.SourceDiscovery = sourceDiscovery
		comparison.EDSDiscovery = edsDiscovery
		comparison.SourcePages = len(sourcePages)
		comparison.EDSPages = len(edsPages)
		comparison.SourceAnalyzed = len(sourcePages)
		comparison.EDSAnalyzed = len(edsPages)
		comparison.FastReady = len(sourcePages) > 0 || len(edsPages) > 0
		comparison.MatchesUpdatedAt = time.Now()
		latestGroups = buildComparisonPagesWithOverrides(mapValues(sourcePages), mapValues(edsPages), overrides)
		comparison = summarizeFastComparison(comparison, latestGroups)
		_ = s.store.ReplaceComparedPages(comparison.ID, latestGroups)
		_ = s.store.UpdateComparison(comparison)
		s.publish(Event{Type: "matches-updated", ScanID: comparison.ID, Message: "Page matches updated", Data: comparison})
	}

	onPage := func(role string) func(PageResult, DiscoveryReport) {
		return func(page PageResult, report DiscoveryReport) {
			_ = s.store.SaveComparisonRolePage(comparison.ID, role, page)
			mu.Lock()
			if role == "source" {
				sourcePages[page.URL] = page
				sourceDiscovery = report
			} else {
				edsPages[page.URL] = page
				edsDiscovery = report
			}
			recomputeLocked("crawling")
			mu.Unlock()
		}
	}

	type crawlDone struct {
		role   string
		result comparisonCrawlResult
	}
	done := make(chan crawlDone, 2)
	go func() {
		done <- crawlDone{role: "source", result: s.crawlForComparison(ctx, comparison.ID, "source", sourceRoot, opts, onPage("source"))}
	}()
	go func() {
		done <- crawlDone{role: "eds", result: s.crawlForComparison(ctx, comparison.ID, "eds", edsRoot, opts, onPage("eds"))}
	}()

	for completed := 0; completed < 2; completed++ {
		select {
		case item := <-done:
			mu.Lock()
			if item.role == "source" {
				sourceDiscovery = item.result.Discovery
				for _, page := range item.result.Pages {
					sourcePages[page.URL] = page
				}
			} else {
				edsDiscovery = item.result.Discovery
				for _, page := range item.result.Pages {
					edsPages[page.URL] = page
				}
			}
			recomputeLocked("matching")
			mu.Unlock()
		case <-ctx.Done():
			s.cancelComparison(comparison)
			return
		}
	}
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}

	mu.Lock()
	recomputeLocked("fast-complete")
	groups := latestGroups
	mu.Unlock()
	s.publish(Event{Type: "fast-complete", ScanID: comparison.ID, Message: "Fast comparison ready", Data: comparison})

	auditTargets := append([]ComparedPage{}, groups.Matched...)
	auditTargets = append(auditTargets, groups.UncertainMatches...)
	auditTargets = s.auditComparison(ctx, comparison.ID, auditTargets, sourceRoot.String(), edsRoot.String(), opts, &comparison)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}
	auditTargets = s.visualCompare(ctx, comparison.ID, autoVisualTargets(auditTargets), &comparison)
	if ctx.Err() != nil {
		s.cancelComparison(comparison)
		return
	}

	comparison.Status = "completed"
	comparison.Phase = "completed"
	comparison.BackgroundPhase = ""
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

func (s *Service) crawlForComparison(ctx context.Context, comparisonID string, role string, root *url.URL, opts ComparisonOptions, onPage func(PageResult, DiscoveryReport)) comparisonCrawlResult {
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
			if onPage != nil {
				onPage(page, NormalizeDiscoveryReport(report))
			}
			if opts.RenderedDiscovery == "always" && page.FetchError == "" && page.ScriptCount > 0 && staticAdded == 0 && !renderedTried[page.URL] {
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
	return buildComparisonPagesWithOverrides(sourcePages, edsPages, nil)
}

func buildComparisonPagesWithOverrides(sourcePages []PageResult, edsPages []PageResult, overrides []MatchOverride) comparisonPageGroups {
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

	overrideState := newComparisonOverrides(overrides)
	usedEDS := map[string]bool{}
	usedSource := map[string]bool{}
	remainingSource := map[string]PageResult{}
	for _, sourceKey := range sortedStringKeys(sourceByPath) {
		edsKey := overrideState.forced[sourceKey]
		if edsKey == "" {
			continue
		}
		source := sourceByPath[sourceKey]
		eds, ok := edsByPath[edsKey]
		if !ok || usedEDS[edsKey] {
			continue
		}
		usedSource[sourceKey] = true
		usedEDS[edsKey] = true
		groups.Matched = append(groups.Matched, comparePagesWithMatch(sourceKey, source, eds, "manual", "high"))
	}
	for _, key := range sortedStringKeys(sourceByPath) {
		source := sourceByPath[key]
		if usedSource[key] {
			continue
		}
		eds, ok := edsByPath[key]
		if ok && !usedEDS[key] && !overrideState.blockedPair(key, key) {
			usedEDS[key] = true
			groups.Matched = append(groups.Matched, comparePagesWithMatch(key, source, eds, "exact", "high"))
			continue
		}
		remainingSource[key] = source
	}

	aliasMaps := buildAliasMaps(edsByPath, usedEDS)
	for _, sourceKey := range sortedStringKeys(remainingSource) {
		source := remainingSource[sourceKey]
		match, ok := findAliasMatch(source, aliasMaps, usedEDS, overrideState, sourceKey)
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
	addMatchCandidates(&groups, sourceByPath, edsByPath, usedEDS)
	return groups
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
		MatchReason:     matchReason(matchType, confidence),
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
	Reason     string
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
		"exact":        {},
		"redirect":     {},
		"canonical":    {},
		"og":           {},
		"path-cleanup": {},
		"locale":       {},
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

func findAliasMatch(source PageResult, edsAliases map[string]map[string]PageResult, usedEDS map[string]bool, overrides comparisonOverrides, sourceKey string) (aliasMatch, bool) {
	edsPriority := []string{"exact", "redirect", "canonical", "og", "path-cleanup", "locale"}
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
			if overrides.blockedPair(sourceKey, edsKey) {
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
	candidates := []aliasCandidate{{Key: comparisonPathKey(page.URL), Type: "exact", Confidence: "high", Reason: "same normalized path"}}
	if key := comparisonPathKey(page.RequestedURL); key != "" && key != candidates[0].Key {
		candidates = append(candidates, aliasCandidate{Key: key, Type: "redirect", Confidence: "high", Reason: "requested URL redirected to the analyzed page"})
	}
	if key := comparisonPathKey(page.Canonical); key != "" && key != "/" {
		candidates = append(candidates, aliasCandidate{Key: key, Type: "canonical", Confidence: "high", Reason: "canonical URL path matched"})
	}
	if key := comparisonPathKey(page.OG.URL); key != "" && key != "/" {
		candidates = append(candidates, aliasCandidate{Key: key, Type: "og", Confidence: "high", Reason: "Open Graph URL path matched"})
	}
	for _, alias := range pathCleanupAliases(candidates[0].Key) {
		candidates = append(candidates, alias)
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
	case "path-cleanup":
		return 4
	case "locale":
		return 5
	default:
		return 6
	}
}

func aliasConfidence(value string) string {
	if value == "exact" || value == "redirect" || value == "canonical" || value == "og" || value == "manual" {
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

type comparisonOverrides struct {
	forced  map[string]string
	blocked map[string]bool
}

func newComparisonOverrides(overrides []MatchOverride) comparisonOverrides {
	state := comparisonOverrides{forced: map[string]string{}, blocked: map[string]bool{}}
	for _, override := range overrides {
		sourceKey := comparisonPathKey(override.SourceURL)
		edsKey := comparisonPathKey(override.EDSURL)
		if sourceKey == "" || edsKey == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(override.Action)) {
		case "match":
			state.forced[sourceKey] = edsKey
			delete(state.blocked, pairKey(sourceKey, edsKey))
		case "unmatch":
			state.blocked[pairKey(sourceKey, edsKey)] = true
			if state.forced[sourceKey] == edsKey {
				delete(state.forced, sourceKey)
			}
		}
	}
	return state
}

func (o comparisonOverrides) blockedPair(sourceKey string, edsKey string) bool {
	return o.blocked[pairKey(sourceKey, edsKey)]
}

func pairKey(sourceKey string, edsKey string) string {
	return sourceKey + "\x00" + edsKey
}

func pathCleanupAliases(key string) []aliasCandidate {
	segments := pathSegments(key)
	seen := map[string]bool{key: true}
	add := func(raw string, typ string, reason string, out *[]aliasCandidate) {
		cleaned := comparisonPathKey(raw)
		if cleaned == "" || cleaned == key || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		*out = append(*out, aliasCandidate{Key: cleaned, Type: typ, Confidence: "medium", Reason: reason})
	}

	var aliases []aliasCandidate
	if strings.HasSuffix(key, ".html") {
		add(strings.TrimSuffix(key, ".html"), "path-cleanup", ".html removed", &aliases)
	}
	if len(segments) > 1 && isLocaleSegment(segments[0]) {
		add("/"+strings.Join(segments[1:], "/"), "locale", "locale prefix removed", &aliases)
		if strings.HasSuffix(key, ".html") {
			add(strings.TrimSuffix("/"+strings.Join(segments[1:], "/"), ".html"), "path-cleanup", "locale prefix and .html suffix removed", &aliases)
		}
	}
	if len(segments) > 1 && isCommonWrapperSegment(segments[0]) {
		add("/"+strings.Join(segments[1:], "/"), "path-cleanup", "common wrapper path removed", &aliases)
	}
	if len(segments) > 2 && isCommonWrapperSegment(segments[0]) {
		add("/"+strings.Join(segments[2:], "/"), "path-cleanup", "common wrapper and site path removed", &aliases)
	}
	if len(segments) > 2 && isCommonWrapperSegment(segments[0]) && isLocaleSegment(segments[2]) {
		add("/"+strings.Join(segments[3:], "/"), "path-cleanup", "wrapper and locale prefixes removed", &aliases)
	}
	return aliases
}

func pathSegments(key string) []string {
	key = strings.Trim(comparisonPathKey(key), "/")
	if key == "" {
		return []string{}
	}
	return strings.Split(key, "/")
}

func isLocaleSegment(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 2 {
		return true
	}
	if len(value) == 5 && (value[2] == '-' || value[2] == '_') {
		return true
	}
	return false
}

func isCommonWrapperSegment(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "content", "contents", "page", "pages", "site", "sites", "www":
		return true
	default:
		return false
	}
}

func matchReason(matchType string, confidence string) string {
	switch matchType {
	case "manual":
		return "Matched manually by user override."
	case "exact":
		return "Matched by exact normalized path."
	case "redirect":
		return "Matched by redirect-final path alias."
	case "canonical":
		return "Matched by canonical URL path."
	case "og":
		return "Matched by Open Graph URL path."
	case "locale":
		return "Matched after removing a locale prefix."
	case "path-cleanup":
		return "Matched after cleaning common path wrappers or .html suffixes."
	default:
		return "Matched by " + confidence + " confidence alias."
	}
}

func addMatchCandidates(groups *comparisonPageGroups, sourceByPath map[string]PageResult, edsByPath map[string]PageResult, usedEDS map[string]bool) {
	unusedEDS := map[string]PageResult{}
	for key, page := range edsByPath {
		if !usedEDS[key] {
			unusedEDS[key] = page
		}
	}
	unusedSource := map[string]PageResult{}
	for _, page := range groups.MissingInEDS {
		unusedSource[comparisonPathKey(page.URL)] = page
	}
	for i := range groups.MissingInEDS {
		groups.MissingInEDS[i].MatchCandidates = suggestMatchCandidates(groups.MissingInEDS[i], unusedEDS)
	}
	for i := range groups.ExtraInEDS {
		groups.ExtraInEDS[i].MatchCandidates = suggestMatchCandidates(groups.ExtraInEDS[i], unusedSource)
	}
}

func suggestMatchCandidates(page PageResult, candidates map[string]PageResult) []MatchCandidate {
	scored := []MatchCandidate{}
	for _, candidate := range candidates {
		score, reasons := matchCandidateScore(page, candidate)
		if score < 35 {
			continue
		}
		scored = append(scored, MatchCandidate{
			URL:    candidate.URL,
			Path:   comparisonPathKey(candidate.URL),
			Title:  candidate.Title,
			H1:     candidate.H1,
			Score:  score,
			Reason: strings.Join(reasons, ", "),
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Path < scored[j].Path
		}
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > 3 {
		return scored[:3]
	}
	return scored
}

func matchCandidateScore(a PageResult, b PageResult) (int, []string) {
	score := 0
	reasons := []string{}
	if lastPathSegment(a.URL) == lastPathSegment(b.URL) && lastPathSegment(a.URL) != "" {
		score += 35
		reasons = append(reasons, "same slug")
	}
	if textKey(a.Title) != "" && textKey(a.Title) == textKey(b.Title) {
		score += 35
		reasons = append(reasons, "same title")
	}
	if textKey(a.H1) != "" && textKey(a.H1) == textKey(b.H1) {
		score += 25
		reasons = append(reasons, "same H1")
	}
	if comparisonPathKey(a.Canonical) != "" && comparisonPathKey(a.Canonical) == comparisonPathKey(b.Canonical) {
		score += 40
		reasons = append(reasons, "same canonical")
	}
	pathScore := tokenOverlap(pathSegments(comparisonPathKey(a.URL)), pathSegments(comparisonPathKey(b.URL)))
	if pathScore >= 50 {
		score += pathScore / 2
		reasons = append(reasons, "similar path")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "similar page signals")
	}
	if score > 100 {
		score = 100
	}
	return score, reasons
}

func textKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func lastPathSegment(raw string) string {
	segments := pathSegments(comparisonPathKey(raw))
	if len(segments) == 0 {
		return ""
	}
	last := segments[len(segments)-1]
	return strings.TrimSuffix(last, ".html")
}

func tokenOverlap(a []string, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, value := range a {
		set[value] = true
	}
	matches := 0
	for _, value := range b {
		if set[value] {
			matches++
		}
	}
	denominator := len(a)
	if len(b) > denominator {
		denominator = len(b)
	}
	return (matches * 100) / denominator
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
	summary.BackgroundPhase = "lighthouse"
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
	summary.BackgroundPhase = "visual-diff"
	summary.VisualQueued = len(matched) * len(DefaultVisualViewports)
	summary.VisualCompleted = 0
	summary.VisualFailed = 0
	summary.VisualReview = 0
	summary.VisualFail = 0
	_ = s.store.UpdateComparison(*summary)
	if len(matched) == 0 {
		return matched
	}
	for i := range matched {
		for _, viewport := range DefaultVisualViewports {
			if ctx.Err() != nil {
				return matched
			}
			visual := s.visual.Diff(ctx, comparisonID, matched[i].Path, matched[i].Source.URL, matched[i].EDS.URL, viewport)
			matched[i].Visuals = append(matched[i].Visuals, visual)
			if visual.Status == "failed" || visual.Status == "error" || visual.Status == "unavailable" {
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
			case "error", "unavailable":
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
	if page.MatchConfidence == "medium" || page.MatchType == "path-cleanup" || page.MatchType == "locale" {
		return "uncertainMatches"
	}
	return "matched"
}

func summarizeFastComparison(summary ComparisonSummary, groups comparisonPageGroups) ComparisonSummary {
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

// autoVisualLimit caps how many matched pages get a visual diff automatically
// after a comparison's fast and Lighthouse phases. The homepage is always
// included; remaining slots go to the highest-severity pages. Anything beyond
// the cap can be run on demand from the dashboard ("Run all").
const autoVisualLimit = 12

// autoVisualTargets selects the matched page pairs to screenshot automatically.
// Only fully fetched pairs qualify (a visual diff needs both screenshots).
func autoVisualTargets(pages []ComparedPage) []ComparedPage {
	pairs := make([]ComparedPage, 0, len(pages))
	for _, page := range pages {
		if page.Source.URL == "" || page.EDS.URL == "" {
			continue
		}
		if page.Source.FetchError != "" || page.EDS.FetchError != "" {
			continue
		}
		pairs = append(pairs, page)
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if (pairs[i].Path == "/") != (pairs[j].Path == "/") {
			return pairs[i].Path == "/"
		}
		if pairs[i].Severity != pairs[j].Severity {
			return pairs[i].Severity > pairs[j].Severity
		}
		return pairs[i].Path < pairs[j].Path
	})
	if len(pairs) > autoVisualLimit {
		pairs = pairs[:autoVisualLimit]
	}
	return pairs
}

func comparisonTargetsByKey(result ComparisonResult, pageKeys []string) []ComparedPage {
	all := append([]ComparedPage{}, result.Matched...)
	all = append(all, result.UncertainMatches...)
	if len(pageKeys) == 0 {
		return all
	}
	wanted := map[string]bool{}
	for _, key := range pageKeys {
		wanted[comparisonPathKey(key)] = true
	}
	targets := []ComparedPage{}
	for _, page := range all {
		if wanted[comparisonPathKey(page.Path)] || wanted[comparisonPathKey(page.Source.URL)] || wanted[comparisonPathKey(page.EDS.URL)] {
			targets = append(targets, page)
		}
	}
	return targets
}

func mapValues(values map[string]PageResult) []PageResult {
	pages := make([]PageResult, 0, len(values))
	for _, page := range values {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].URL < pages[j].URL })
	return pages
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

// comparisonPathKey reduces a URL to the path identity used to match a legacy
// page against its migrated EDS page. Host, scheme, query, and fragment are
// dropped; the path is lowercased and trailing slashes removed. Crucially, a
// trailing .html/.htm extension is stripped (legacy "about.html" matches EDS
// "about") and directory index pages collapse onto their parent ("/foo/index"
// -> "/foo", "/index.html" -> "/").
func comparisonPathKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	path := raw
	if parsed, err := url.Parse(raw); err == nil {
		path = parsed.Path
	}
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	lower := strings.ToLower(path)
	// Legacy sites commonly serve .html/.htm pages that migrate to extensionless
	// EDS paths, so drop the extension before matching.
	if strings.HasSuffix(lower, ".html") {
		lower = strings.TrimSuffix(lower, ".html")
	} else if strings.HasSuffix(lower, ".htm") {
		lower = strings.TrimSuffix(lower, ".htm")
	}
	// Collapse directory index pages onto their parent path.
	if lower == "/index" {
		return "/"
	}
	if strings.HasSuffix(lower, "/index") {
		lower = strings.TrimSuffix(lower, "/index")
	}
	if lower == "" {
		return "/"
	}
	return lower
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

func pageStatusFromSeverity(severity int) string {
	if severity >= 6 {
		return "fail"
	}
	if severity > 0 {
		return "review"
	}
	return "pass"
}
