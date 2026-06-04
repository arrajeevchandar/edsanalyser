package scanner

import "strings"

func NormalizeScanResult(result ScanResult) ScanResult {
	if result.Pages == nil {
		result.Pages = []PageResult{}
	}
	if result.Blocks == nil {
		result.Blocks = []BlockStat{}
	}
	if result.Sections == nil {
		result.Sections = []SectionStat{}
	}
	for i := range result.Pages {
		result.Pages[i] = NormalizePage(result.Pages[i])
	}
	for i := range result.Blocks {
		if result.Blocks[i].Variations == nil {
			result.Blocks[i].Variations = map[string]int{}
		}
		if result.Blocks[i].Pages == nil {
			result.Blocks[i].Pages = []string{}
		}
	}
	for i := range result.Sections {
		if result.Sections[i].Pages == nil {
			result.Sections[i].Pages = []string{}
		}
	}
	return result
}

func NormalizeComparisonResult(result ComparisonResult) ComparisonResult {
	if result.Matched == nil {
		result.Matched = []ComparedPage{}
	}
	if result.UncertainMatches == nil {
		result.UncertainMatches = []ComparedPage{}
	}
	if result.MissingInEDS == nil {
		result.MissingInEDS = []PageResult{}
	}
	if result.ExtraInEDS == nil {
		result.ExtraInEDS = []PageResult{}
	}
	if result.SourceFetchFailures == nil {
		result.SourceFetchFailures = []PageResult{}
	}
	if result.EDSFetchFailures == nil {
		result.EDSFetchFailures = []PageResult{}
	}
	if result.Blocks == nil {
		result.Blocks = []BlockStat{}
	}
	if result.Sections == nil {
		result.Sections = []SectionStat{}
	}
	result.Discovery.Source = NormalizeDiscoveryReport(result.Discovery.Source)
	result.Discovery.EDS = NormalizeDiscoveryReport(result.Discovery.EDS)
	for i := range result.Matched {
		result.Matched[i] = NormalizeComparedPage(result.Matched[i])
	}
	for i := range result.UncertainMatches {
		result.UncertainMatches[i] = NormalizeComparedPage(result.UncertainMatches[i])
	}
	for i := range result.MissingInEDS {
		result.MissingInEDS[i] = NormalizePage(result.MissingInEDS[i])
	}
	for i := range result.ExtraInEDS {
		result.ExtraInEDS[i] = NormalizePage(result.ExtraInEDS[i])
	}
	for i := range result.SourceFetchFailures {
		result.SourceFetchFailures[i] = NormalizePage(result.SourceFetchFailures[i])
	}
	for i := range result.EDSFetchFailures {
		result.EDSFetchFailures[i] = NormalizePage(result.EDSFetchFailures[i])
	}
	for i := range result.Blocks {
		if result.Blocks[i].Variations == nil {
			result.Blocks[i].Variations = map[string]int{}
		}
		if result.Blocks[i].Pages == nil {
			result.Blocks[i].Pages = []string{}
		}
	}
	for i := range result.Sections {
		if result.Sections[i].Pages == nil {
			result.Sections[i].Pages = []string{}
		}
	}
	return result
}

func NormalizeComparedPage(page ComparedPage) ComparedPage {
	page.Source = NormalizePage(page.Source)
	page.EDS = NormalizePage(page.EDS)
	if page.MatchType == "" {
		page.MatchType = "exact"
	}
	if page.MatchConfidence == "" {
		page.MatchConfidence = "high"
	}
	if page.MatchReason == "" {
		page.MatchReason = page.MatchType
	}
	if page.SourceAliases == nil {
		page.SourceAliases = []string{}
	}
	if page.EDSAliases == nil {
		page.EDSAliases = []string{}
	}
	if page.FieldDiffs == nil {
		page.FieldDiffs = []FieldDiff{}
	}
	if page.LinkDiffs == nil {
		page.LinkDiffs = []FieldDiff{}
	}
	if page.Visuals == nil {
		page.Visuals = []VisualDiff{}
	}
	if page.Issues == nil {
		page.Issues = []string{}
	}
	if page.Status == "" {
		page.Status = "pass"
	}
	return page
}

func NormalizeDiscoveryReport(report DiscoveryReport) DiscoveryReport {
	if report.Warnings == nil {
		report.Warnings = []string{}
	}
	return report
}

func NormalizePage(page PageResult) PageResult {
	if page.RequestedURL == "" {
		page.RequestedURL = page.URL
	}
	if page.Links == nil {
		page.Links = []LinkInfo{}
	}
	if page.Blocks == nil {
		page.Blocks = []BlockInfo{}
	}
	if page.Sections == nil {
		page.Sections = []SectionInfo{}
	}
	if page.MatchCandidates == nil {
		page.MatchCandidates = []MatchCandidate{}
	}
	if page.AuditStatus == "" {
		switch {
		case page.AuditError != "":
			page.AuditStatus = "failed"
		case page.Lighthouse.Health != nil:
			page.AuditStatus = "complete"
		default:
			page.AuditStatus = "pending"
		}
	}
	if page.AuditStatus == "failed" && isCanceledAuditError(page.AuditError) {
		page.AuditStatus = "pending"
		page.AuditError = ""
	}
	for i := range page.Blocks {
		if page.Blocks[i].Variations == nil {
			page.Blocks[i].Variations = []string{}
		}
	}
	for i := range page.Sections {
		if page.Sections[i].Variations == nil {
			page.Sections[i].Variations = []string{}
		}
		if page.Sections[i].Blocks == nil {
			page.Sections[i].Blocks = []string{}
		}
	}
	return page
}

func isCanceledAuditError(value string) bool {
	return strings.Contains(strings.ToLower(value), "context canceled")
}
