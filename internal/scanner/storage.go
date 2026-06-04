package scanner

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store interface {
	CreateScan(ScanSummary) error
	UpdateScan(ScanSummary) error
	SavePage(string, PageResult) error
	ListScans() ([]ScanSummary, error)
	GetScan(string) (ScanResult, error)
	CreateComparison(ComparisonSummary) error
	UpdateComparison(ComparisonSummary) error
	SaveComparisonRolePage(string, string, PageResult) error
	ListComparisonRolePages(string, string) ([]PageResult, error)
	SaveComparedPage(string, string, ComparedPage) error
	ReplaceComparedPages(string, comparisonPageGroups) error
	SaveComparisonVisual(string, string, VisualDiff) error
	SaveComparisonMatchOverride(string, MatchOverride) error
	ListComparisonMatchOverrides(string) ([]MatchOverride, error)
	ListComparisons() ([]ComparisonSummary, error)
	GetComparison(string) (ComparisonResult, error)
	Close() error
}

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &SQLiteStore{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) init() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS scans (
  id TEXT PRIMARY KEY,
  input_url TEXT NOT NULL,
  root_url TEXT NOT NULL,
  status TEXT NOT NULL,
  phase TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT,
  discovered_pages INTEGER NOT NULL DEFAULT 0,
  completed_pages INTEGER NOT NULL DEFAULT 0,
  failed_pages INTEGER NOT NULL DEFAULT 0,
  fast_completed_pages INTEGER NOT NULL DEFAULT 0,
  audit_queued_pages INTEGER NOT NULL DEFAULT 0,
  audit_completed_pages INTEGER NOT NULL DEFAULT 0,
  audit_failed_pages INTEGER NOT NULL DEFAULT 0,
  performance REAL,
  accessibility REAL,
  best_practices REAL,
  seo REAL,
  health REAL,
  error TEXT
);
CREATE TABLE IF NOT EXISTS pages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  scan_id TEXT NOT NULL,
  url TEXT NOT NULL,
  status_code INTEGER,
  title TEXT,
  h1 TEXT,
  canonical TEXT,
  description TEXT,
  robots TEXT,
  lang TEXT,
  og_json TEXT NOT NULL,
  links_json TEXT NOT NULL,
  blocks_json TEXT NOT NULL,
  sections_json TEXT NOT NULL,
  block_count INTEGER NOT NULL,
  section_count INTEGER NOT NULL,
  link_count INTEGER NOT NULL,
  internal_links INTEGER NOT NULL,
  external_links INTEGER NOT NULL,
  performance REAL,
  accessibility REAL,
  best_practices REAL,
  seo REAL,
  health REAL,
  audit_status TEXT NOT NULL DEFAULT '',
  audit_error TEXT,
  fetch_error TEXT,
  UNIQUE(scan_id, url)
);
CREATE TABLE IF NOT EXISTS comparisons (
  id TEXT PRIMARY KEY,
  source_input_url TEXT NOT NULL,
  eds_input_url TEXT NOT NULL,
  source_root_url TEXT NOT NULL,
  eds_root_url TEXT NOT NULL,
  status TEXT NOT NULL,
  phase TEXT NOT NULL DEFAULT '',
  fast_ready INTEGER NOT NULL DEFAULT 0,
  background_phase TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT,
  source_pages INTEGER NOT NULL DEFAULT 0,
  eds_pages INTEGER NOT NULL DEFAULT 0,
  source_analyzed INTEGER NOT NULL DEFAULT 0,
  eds_analyzed INTEGER NOT NULL DEFAULT 0,
  matches_updated_at TEXT,
  matched_pages INTEGER NOT NULL DEFAULT 0,
  uncertain_matches INTEGER NOT NULL DEFAULT 0,
  missing_in_eds INTEGER NOT NULL DEFAULT 0,
  extra_in_eds INTEGER NOT NULL DEFAULT 0,
  source_fetch_failures INTEGER NOT NULL DEFAULT 0,
  eds_fetch_failures INTEGER NOT NULL DEFAULT 0,
  metadata_diffs INTEGER NOT NULL DEFAULT 0,
  link_diffs INTEGER NOT NULL DEFAULT 0,
  visual_queued INTEGER NOT NULL DEFAULT 0,
  visual_completed INTEGER NOT NULL DEFAULT 0,
  visual_failed INTEGER NOT NULL DEFAULT 0,
  visual_review INTEGER NOT NULL DEFAULT 0,
  visual_fail INTEGER NOT NULL DEFAULT 0,
  lighthouse_queued INTEGER NOT NULL DEFAULT 0,
  lighthouse_completed INTEGER NOT NULL DEFAULT 0,
  lighthouse_failed INTEGER NOT NULL DEFAULT 0,
  migration_score REAL,
  source_discovery_json TEXT NOT NULL DEFAULT '{}',
  eds_discovery_json TEXT NOT NULL DEFAULT '{}',
  error TEXT
);
CREATE TABLE IF NOT EXISTS comparison_role_pages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  comparison_id TEXT NOT NULL,
  role TEXT NOT NULL,
  url TEXT NOT NULL,
  page_json TEXT NOT NULL,
  UNIQUE(comparison_id, role, url)
);
CREATE TABLE IF NOT EXISTS comparison_pages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  comparison_id TEXT NOT NULL,
  page_key TEXT NOT NULL,
  group_name TEXT NOT NULL,
  status TEXT NOT NULL,
  severity INTEGER NOT NULL DEFAULT 0,
  match_type TEXT NOT NULL DEFAULT '',
  match_confidence TEXT NOT NULL DEFAULT '',
  match_reason TEXT NOT NULL DEFAULT '',
  source_aliases_json TEXT NOT NULL DEFAULT '[]',
  eds_aliases_json TEXT NOT NULL DEFAULT '[]',
  source_json TEXT NOT NULL,
  eds_json TEXT NOT NULL,
  field_diffs_json TEXT NOT NULL,
  link_diffs_json TEXT NOT NULL,
  issues_json TEXT NOT NULL,
  UNIQUE(comparison_id, page_key, group_name)
);
CREATE TABLE IF NOT EXISTS comparison_visuals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  comparison_id TEXT NOT NULL,
  page_key TEXT NOT NULL,
  viewport TEXT NOT NULL,
  source_image TEXT NOT NULL DEFAULT '',
  eds_image TEXT NOT NULL DEFAULT '',
  diff_image TEXT NOT NULL DEFAULT '',
  diff_percent REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  error TEXT,
  UNIQUE(comparison_id, page_key, viewport)
);
CREATE TABLE IF NOT EXISTS comparison_match_overrides (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  comparison_id TEXT NOT NULL,
  source_url TEXT NOT NULL,
  eds_url TEXT NOT NULL,
  action TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(comparison_id, source_url, eds_url)
);`)
	if err != nil {
		return err
	}
	for _, column := range []struct {
		table string
		name  string
		def   string
	}{
		{"scans", "phase", "TEXT NOT NULL DEFAULT ''"},
		{"scans", "fast_completed_pages", "INTEGER NOT NULL DEFAULT 0"},
		{"scans", "audit_queued_pages", "INTEGER NOT NULL DEFAULT 0"},
		{"scans", "audit_completed_pages", "INTEGER NOT NULL DEFAULT 0"},
		{"scans", "audit_failed_pages", "INTEGER NOT NULL DEFAULT 0"},
		{"pages", "audit_status", "TEXT NOT NULL DEFAULT ''"},
		{"comparisons", "fast_ready", "INTEGER NOT NULL DEFAULT 0"},
		{"comparisons", "background_phase", "TEXT NOT NULL DEFAULT ''"},
		{"comparisons", "source_analyzed", "INTEGER NOT NULL DEFAULT 0"},
		{"comparisons", "eds_analyzed", "INTEGER NOT NULL DEFAULT 0"},
		{"comparisons", "matches_updated_at", "TEXT"},
		{"comparisons", "uncertain_matches", "INTEGER NOT NULL DEFAULT 0"},
		{"comparisons", "source_discovery_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"comparisons", "eds_discovery_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"comparison_pages", "match_type", "TEXT NOT NULL DEFAULT ''"},
		{"comparison_pages", "match_confidence", "TEXT NOT NULL DEFAULT ''"},
		{"comparison_pages", "match_reason", "TEXT NOT NULL DEFAULT ''"},
		{"comparison_pages", "source_aliases_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"comparison_pages", "eds_aliases_json", "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.def); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) CreateScan(scan ScanSummary) error {
	_, err := s.db.Exec(`
INSERT INTO scans (id, input_url, root_url, status, phase, started_at, discovered_pages, completed_pages, failed_pages,
  fast_completed_pages, audit_queued_pages, audit_completed_pages, audit_failed_pages, performance, accessibility, best_practices, seo, health, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		scan.ID, scan.InputURL, scan.RootURL, scan.Status, scan.Phase, scan.StartedAt.Format(time.RFC3339Nano),
		scan.DiscoveredPages, scan.CompletedPages, scan.FailedPages, scan.FastCompletedPages,
		scan.AuditQueuedPages, scan.AuditCompletedPages, scan.AuditFailedPages,
		nullable(scan.Scores.Performance), nullable(scan.Scores.Accessibility), nullable(scan.Scores.BestPractices), nullable(scan.Scores.SEO), nullable(scan.Scores.Health),
		scan.Error)
	return err
}

func (s *SQLiteStore) UpdateScan(scan ScanSummary) error {
	var finished any
	if !scan.FinishedAt.IsZero() {
		finished = scan.FinishedAt.Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`
UPDATE scans
SET status = ?, phase = ?, finished_at = ?, discovered_pages = ?, completed_pages = ?, failed_pages = ?,
    fast_completed_pages = ?, audit_queued_pages = ?, audit_completed_pages = ?, audit_failed_pages = ?,
    performance = ?, accessibility = ?, best_practices = ?, seo = ?, health = ?, error = ?
WHERE id = ?`,
		scan.Status, scan.Phase, finished, scan.DiscoveredPages, scan.CompletedPages, scan.FailedPages,
		scan.FastCompletedPages, scan.AuditQueuedPages, scan.AuditCompletedPages, scan.AuditFailedPages,
		nullable(scan.Scores.Performance), nullable(scan.Scores.Accessibility), nullable(scan.Scores.BestPractices), nullable(scan.Scores.SEO), nullable(scan.Scores.Health),
		scan.Error, scan.ID)
	return err
}

func (s *SQLiteStore) SavePage(scanID string, page PageResult) error {
	page = NormalizePage(page)
	og, _ := json.Marshal(page.OG)
	links, _ := json.Marshal(page.Links)
	blocks, _ := json.Marshal(page.Blocks)
	sections, _ := json.Marshal(page.Sections)
	_, err := s.db.Exec(`
INSERT INTO pages (
  scan_id, url, status_code, title, h1, canonical, description, robots, lang,
  og_json, links_json, blocks_json, sections_json, block_count, section_count, link_count,
  internal_links, external_links, performance, accessibility, best_practices, seo, health, audit_status,
  audit_error, fetch_error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scan_id, url) DO UPDATE SET
  status_code=excluded.status_code, title=excluded.title, h1=excluded.h1, canonical=excluded.canonical,
  description=excluded.description, robots=excluded.robots, lang=excluded.lang, og_json=excluded.og_json,
  links_json=excluded.links_json, blocks_json=excluded.blocks_json, sections_json=excluded.sections_json,
  block_count=excluded.block_count, section_count=excluded.section_count, link_count=excluded.link_count,
  internal_links=excluded.internal_links, external_links=excluded.external_links, performance=excluded.performance,
  accessibility=excluded.accessibility, best_practices=excluded.best_practices, seo=excluded.seo,
  health=excluded.health, audit_status=excluded.audit_status, audit_error=excluded.audit_error, fetch_error=excluded.fetch_error`,
		scanID, page.URL, page.StatusCode, page.Title, page.H1, page.Canonical, page.Description, page.Robots, page.Lang,
		string(og), string(links), string(blocks), string(sections), page.BlockCount, page.SectionCount, page.LinkCount,
		page.InternalLinks, page.ExternalLinks, nullable(page.Lighthouse.Performance), nullable(page.Lighthouse.Accessibility),
		nullable(page.Lighthouse.BestPractices), nullable(page.Lighthouse.SEO), nullable(page.Lighthouse.Health),
		page.AuditStatus, page.AuditError, page.FetchError)
	return err
}

func (s *SQLiteStore) ListScans() ([]ScanSummary, error) {
	rows, err := s.db.Query(`
SELECT id, input_url, root_url, status, COALESCE(phase, ''), started_at, COALESCE(finished_at, ''), discovered_pages, completed_pages, failed_pages,
       fast_completed_pages, audit_queued_pages, audit_completed_pages, audit_failed_pages,
       performance, accessibility, best_practices, seo, health, COALESCE(error, '')
FROM scans ORDER BY started_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}

	scans := []ScanSummary{}
	for rows.Next() {
		scan, err := scanFromRows(rows)
		if err != nil {
			return nil, err
		}
		scans = append(scans, scan)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range scans {
		if err := s.recomputeStoredSummary(&scans[i]); err != nil {
			return nil, err
		}
	}
	return scans, nil
}

func (s *SQLiteStore) GetScan(id string) (ScanResult, error) {
	row := s.db.QueryRow(`
SELECT id, input_url, root_url, status, COALESCE(phase, ''), started_at, COALESCE(finished_at, ''), discovered_pages, completed_pages, failed_pages,
       fast_completed_pages, audit_queued_pages, audit_completed_pages, audit_failed_pages,
       performance, accessibility, best_practices, seo, health, COALESCE(error, '')
FROM scans WHERE id = ?`, id)
	summary, err := scanFromRows(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScanResult{}, err
		}
		return ScanResult{}, err
	}

	rows, err := s.db.Query(`
SELECT url, status_code, COALESCE(title, ''), COALESCE(h1, ''), COALESCE(canonical, ''), COALESCE(description, ''),
       COALESCE(robots, ''), COALESCE(lang, ''), og_json, links_json, blocks_json, sections_json,
       block_count, section_count, link_count, internal_links, external_links,
       performance, accessibility, best_practices, seo, health, COALESCE(audit_status, ''), COALESCE(audit_error, ''), COALESCE(fetch_error, '')
FROM pages WHERE scan_id = ? ORDER BY url`, id)
	if err != nil {
		return ScanResult{}, err
	}
	defer rows.Close()

	result := ScanResult{
		Summary:     summary,
		Pages:       []PageResult{},
		Blocks:      []BlockStat{},
		Sections:    []SectionStat{},
		GeneratedAt: time.Now(),
	}
	for rows.Next() {
		page, err := pageFromRows(rows)
		if err != nil {
			return ScanResult{}, err
		}
		result.Pages = append(result.Pages, page)
	}
	if err := rows.Err(); err != nil {
		return ScanResult{}, err
	}
	result.Summary = recomputeSummaryFromPages(result.Summary, result.Pages)
	result.Blocks, result.Sections, result.Links, result.SEO = aggregate(result.Pages)
	return NormalizeScanResult(result), nil
}

func (s *SQLiteStore) CreateComparison(comparison ComparisonSummary) error {
	sourceDiscovery, _ := json.Marshal(NormalizeDiscoveryReport(comparison.SourceDiscovery))
	edsDiscovery, _ := json.Marshal(NormalizeDiscoveryReport(comparison.EDSDiscovery))
	var matchesUpdated any
	if !comparison.MatchesUpdatedAt.IsZero() {
		matchesUpdated = comparison.MatchesUpdatedAt.Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`
INSERT INTO comparisons (
  id, source_input_url, eds_input_url, source_root_url, eds_root_url, status, phase, fast_ready, background_phase, started_at,
  source_pages, eds_pages, source_analyzed, eds_analyzed, matches_updated_at,
  matched_pages, uncertain_matches, missing_in_eds, extra_in_eds, source_fetch_failures, eds_fetch_failures,
  metadata_diffs, link_diffs, visual_queued, visual_completed, visual_failed, visual_review, visual_fail,
  lighthouse_queued, lighthouse_completed, lighthouse_failed, migration_score, source_discovery_json, eds_discovery_json, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		comparison.ID, comparison.SourceInputURL, comparison.EDSInputURL, comparison.SourceRootURL, comparison.EDSRootURL,
		comparison.Status, comparison.Phase, boolInt(comparison.FastReady), comparison.BackgroundPhase, comparison.StartedAt.Format(time.RFC3339Nano),
		comparison.SourcePages, comparison.EDSPages, comparison.SourceAnalyzed, comparison.EDSAnalyzed, matchesUpdated,
		comparison.MatchedPages, comparison.UncertainMatches, comparison.MissingInEDS, comparison.ExtraInEDS,
		comparison.SourceFetchFailures, comparison.EDSFetchFailures, comparison.MetadataDiffs, comparison.LinkDiffs,
		comparison.VisualQueued, comparison.VisualCompleted, comparison.VisualFailed, comparison.VisualReview, comparison.VisualFail,
		comparison.LighthouseQueued, comparison.LighthouseCompleted, comparison.LighthouseFailed, nullable(comparison.MigrationScore),
		string(sourceDiscovery), string(edsDiscovery), comparison.Error)
	return err
}

func (s *SQLiteStore) UpdateComparison(comparison ComparisonSummary) error {
	var finished any
	if !comparison.FinishedAt.IsZero() {
		finished = comparison.FinishedAt.Format(time.RFC3339Nano)
	}
	var matchesUpdated any
	if !comparison.MatchesUpdatedAt.IsZero() {
		matchesUpdated = comparison.MatchesUpdatedAt.Format(time.RFC3339Nano)
	}
	sourceDiscovery, _ := json.Marshal(NormalizeDiscoveryReport(comparison.SourceDiscovery))
	edsDiscovery, _ := json.Marshal(NormalizeDiscoveryReport(comparison.EDSDiscovery))
	_, err := s.db.Exec(`
UPDATE comparisons
SET status = ?, phase = ?, fast_ready = ?, background_phase = ?, finished_at = ?, source_pages = ?, eds_pages = ?,
    source_analyzed = ?, eds_analyzed = ?, matches_updated_at = ?, matched_pages = ?,
    uncertain_matches = ?, missing_in_eds = ?, extra_in_eds = ?, source_fetch_failures = ?, eds_fetch_failures = ?,
    metadata_diffs = ?, link_diffs = ?, visual_queued = ?, visual_completed = ?, visual_failed = ?,
    visual_review = ?, visual_fail = ?, lighthouse_queued = ?, lighthouse_completed = ?, lighthouse_failed = ?,
    migration_score = ?, source_discovery_json = ?, eds_discovery_json = ?, error = ?
WHERE id = ?`,
		comparison.Status, comparison.Phase, boolInt(comparison.FastReady), comparison.BackgroundPhase, finished,
		comparison.SourcePages, comparison.EDSPages, comparison.SourceAnalyzed, comparison.EDSAnalyzed, matchesUpdated, comparison.MatchedPages,
		comparison.UncertainMatches, comparison.MissingInEDS, comparison.ExtraInEDS, comparison.SourceFetchFailures, comparison.EDSFetchFailures,
		comparison.MetadataDiffs, comparison.LinkDiffs, comparison.VisualQueued, comparison.VisualCompleted, comparison.VisualFailed,
		comparison.VisualReview, comparison.VisualFail, comparison.LighthouseQueued, comparison.LighthouseCompleted, comparison.LighthouseFailed,
		nullable(comparison.MigrationScore), string(sourceDiscovery), string(edsDiscovery), comparison.Error, comparison.ID)
	return err
}

func (s *SQLiteStore) SaveComparisonRolePage(comparisonID string, role string, page PageResult) error {
	page = NormalizePage(page)
	payload, _ := json.Marshal(page)
	_, err := s.db.Exec(`
INSERT INTO comparison_role_pages (comparison_id, role, url, page_json)
VALUES (?, ?, ?, ?)
ON CONFLICT(comparison_id, role, url) DO UPDATE SET page_json=excluded.page_json`,
		comparisonID, role, page.URL, string(payload))
	return err
}

func (s *SQLiteStore) ListComparisonRolePages(comparisonID string, role string) ([]PageResult, error) {
	rows, err := s.db.Query(`
SELECT page_json FROM comparison_role_pages WHERE comparison_id = ? AND role = ? ORDER BY url`, comparisonID, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pages := []PageResult{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var page PageResult
		_ = json.Unmarshal([]byte(payload), &page)
		pages = append(pages, NormalizePage(page))
	}
	return pages, rows.Err()
}

func (s *SQLiteStore) SaveComparedPage(comparisonID string, group string, page ComparedPage) error {
	return saveComparedPage(s.db, comparisonID, group, page)
}

func (s *SQLiteStore) ReplaceComparedPages(comparisonID string, groups comparisonPageGroups) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM comparison_pages WHERE comparison_id = ?`, comparisonID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, page := range groups.Matched {
		if err := saveComparedPage(tx, comparisonID, "matched", page); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, page := range groups.UncertainMatches {
		if err := saveComparedPage(tx, comparisonID, "uncertainMatches", page); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, page := range groups.MissingInEDS {
		row := ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, MatchType: "unmatched", MatchConfidence: "low", MatchReason: "No EDS page matched this source page.", Source: page, Issues: []string{"Missing in migrated EDS site"}}
		if err := saveComparedPage(tx, comparisonID, "missingInEDS", row); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, page := range groups.ExtraInEDS {
		row := ComparedPage{Path: comparisonPathKey(page.URL), Status: "review", Severity: 4, MatchType: "unmatched", MatchConfidence: "low", MatchReason: "No legacy source page matched this EDS page.", EDS: page, Issues: []string{"Extra page in migrated EDS site"}}
		if err := saveComparedPage(tx, comparisonID, "extraInEDS", row); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, page := range groups.SourceFetchFailures {
		row := ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, MatchType: "unmatched", MatchConfidence: "low", MatchReason: "Source fetch failed.", Source: page, Issues: []string{page.FetchError}}
		if err := saveComparedPage(tx, comparisonID, "sourceFetchFailures", row); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, page := range groups.EDSFetchFailures {
		row := ComparedPage{Path: comparisonPathKey(page.URL), Status: "fail", Severity: 10, MatchType: "unmatched", MatchConfidence: "low", MatchReason: "EDS fetch failed.", EDS: page, Issues: []string{page.FetchError}}
		if err := saveComparedPage(tx, comparisonID, "edsFetchFailures", row); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func saveComparedPage(exec sqlExecer, comparisonID string, group string, page ComparedPage) error {
	page = NormalizeComparedPage(page)
	source, _ := json.Marshal(page.Source)
	eds, _ := json.Marshal(page.EDS)
	fields, _ := json.Marshal(page.FieldDiffs)
	links, _ := json.Marshal(page.LinkDiffs)
	issues, _ := json.Marshal(page.Issues)
	sourceAliases, _ := json.Marshal(page.SourceAliases)
	edsAliases, _ := json.Marshal(page.EDSAliases)
	_, err := exec.Exec(`
INSERT INTO comparison_pages (
  comparison_id, page_key, group_name, status, severity, match_type, match_confidence, match_reason, source_aliases_json, eds_aliases_json,
  source_json, eds_json, field_diffs_json, link_diffs_json, issues_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(comparison_id, page_key, group_name) DO UPDATE SET
  status=excluded.status, severity=excluded.severity, match_type=excluded.match_type, match_confidence=excluded.match_confidence,
  match_reason=excluded.match_reason,
  source_aliases_json=excluded.source_aliases_json, eds_aliases_json=excluded.eds_aliases_json,
  source_json=excluded.source_json, eds_json=excluded.eds_json,
  field_diffs_json=excluded.field_diffs_json, link_diffs_json=excluded.link_diffs_json, issues_json=excluded.issues_json`,
		comparisonID, page.Path, group, page.Status, page.Severity, page.MatchType, page.MatchConfidence, page.MatchReason, string(sourceAliases), string(edsAliases),
		string(source), string(eds), string(fields), string(links), string(issues))
	return err
}

func (s *SQLiteStore) SaveComparisonVisual(comparisonID string, pageKey string, visual VisualDiff) error {
	_, err := s.db.Exec(`
INSERT INTO comparison_visuals (
  comparison_id, page_key, viewport, source_image, eds_image, diff_image, diff_percent, status, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(comparison_id, page_key, viewport) DO UPDATE SET
  source_image=excluded.source_image, eds_image=excluded.eds_image, diff_image=excluded.diff_image,
  diff_percent=excluded.diff_percent, status=excluded.status, error=excluded.error`,
		comparisonID, pageKey, visual.Viewport, visual.SourceImage, visual.EDSImage, visual.DiffImage, visual.DiffPercent, visual.Status, visual.Error)
	return err
}

func (s *SQLiteStore) SaveComparisonMatchOverride(comparisonID string, override MatchOverride) error {
	override.Action = strings.ToLower(strings.TrimSpace(override.Action))
	if override.Action != "match" && override.Action != "unmatch" {
		return errors.New("match override action must be match or unmatch")
	}
	_, err := s.db.Exec(`
INSERT INTO comparison_match_overrides (comparison_id, source_url, eds_url, action, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(comparison_id, source_url, eds_url) DO UPDATE SET action=excluded.action, created_at=excluded.created_at`,
		comparisonID, strings.TrimSpace(override.SourceURL), strings.TrimSpace(override.EDSURL), override.Action, time.Now().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ListComparisonMatchOverrides(comparisonID string) ([]MatchOverride, error) {
	rows, err := s.db.Query(`
SELECT source_url, eds_url, action FROM comparison_match_overrides WHERE comparison_id = ? ORDER BY created_at`, comparisonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	overrides := []MatchOverride{}
	for rows.Next() {
		var override MatchOverride
		if err := rows.Scan(&override.SourceURL, &override.EDSURL, &override.Action); err != nil {
			return nil, err
		}
		overrides = append(overrides, override)
	}
	return overrides, rows.Err()
}

func (s *SQLiteStore) ListComparisons() ([]ComparisonSummary, error) {
	rows, err := s.db.Query(`
SELECT id, source_input_url, eds_input_url, source_root_url, eds_root_url, status, COALESCE(phase, ''),
       COALESCE(fast_ready, 0), COALESCE(background_phase, ''), started_at, COALESCE(finished_at, ''),
       source_pages, eds_pages, COALESCE(source_analyzed, source_pages), COALESCE(eds_analyzed, eds_pages), COALESCE(matches_updated_at, ''),
       matched_pages, uncertain_matches, missing_in_eds, extra_in_eds,
       source_fetch_failures, eds_fetch_failures, metadata_diffs, link_diffs, visual_queued, visual_completed,
       visual_failed, visual_review, visual_fail, lighthouse_queued, lighthouse_completed, lighthouse_failed,
       migration_score, COALESCE(source_discovery_json, '{}'), COALESCE(eds_discovery_json, '{}'), COALESCE(error, '')
FROM comparisons ORDER BY started_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	comparisons := []ComparisonSummary{}
	for rows.Next() {
		comparison, err := comparisonFromRows(rows)
		if err != nil {
			return nil, err
		}
		comparisons = append(comparisons, comparison)
	}
	return comparisons, rows.Err()
}

func (s *SQLiteStore) GetComparison(id string) (ComparisonResult, error) {
	row := s.db.QueryRow(`
SELECT id, source_input_url, eds_input_url, source_root_url, eds_root_url, status, COALESCE(phase, ''),
       COALESCE(fast_ready, 0), COALESCE(background_phase, ''), started_at, COALESCE(finished_at, ''),
       source_pages, eds_pages, COALESCE(source_analyzed, source_pages), COALESCE(eds_analyzed, eds_pages), COALESCE(matches_updated_at, ''),
       matched_pages, uncertain_matches, missing_in_eds, extra_in_eds,
       source_fetch_failures, eds_fetch_failures, metadata_diffs, link_diffs, visual_queued, visual_completed,
       visual_failed, visual_review, visual_fail, lighthouse_queued, lighthouse_completed, lighthouse_failed,
       migration_score, COALESCE(source_discovery_json, '{}'), COALESCE(eds_discovery_json, '{}'), COALESCE(error, '')
FROM comparisons WHERE id = ?`, id)
	summary, err := comparisonFromRows(row)
	if err != nil {
		return ComparisonResult{}, err
	}

	rows, err := s.db.Query(`
SELECT page_key, group_name, status, severity, COALESCE(match_type, ''), COALESCE(match_confidence, ''),
       COALESCE(match_reason, ''), COALESCE(source_aliases_json, '[]'), COALESCE(eds_aliases_json, '[]'),
       source_json, eds_json, field_diffs_json, link_diffs_json, issues_json
FROM comparison_pages WHERE comparison_id = ? ORDER BY group_name, page_key`, id)
	if err != nil {
		return ComparisonResult{}, err
	}

	result := ComparisonResult{
		Summary:             summary,
		Discovery:           ComparisonDiscovery{Source: summary.SourceDiscovery, EDS: summary.EDSDiscovery},
		Matched:             []ComparedPage{},
		UncertainMatches:    []ComparedPage{},
		MissingInEDS:        []PageResult{},
		ExtraInEDS:          []PageResult{},
		SourceFetchFailures: []PageResult{},
		EDSFetchFailures:    []PageResult{},
		Blocks:              []BlockStat{},
		Sections:            []SectionStat{},
		GeneratedAt:         time.Now(),
	}

	var edsPages []PageResult
	for rows.Next() {
		page, group, err := comparedPageFromRows(rows)
		if err != nil {
			_ = rows.Close()
			return ComparisonResult{}, err
		}
		page = NormalizeComparedPage(page)
		switch group {
		case "matched":
			result.Matched = append(result.Matched, page)
			result.Links.MatchedPageDiffs += len(page.LinkDiffs)
			edsPages = append(edsPages, page.EDS)
		case "uncertainMatches":
			result.UncertainMatches = append(result.UncertainMatches, page)
			result.Links.MatchedPageDiffs += len(page.LinkDiffs)
			edsPages = append(edsPages, page.EDS)
		case "missingInEDS":
			result.MissingInEDS = append(result.MissingInEDS, page.Source)
		case "extraInEDS":
			result.ExtraInEDS = append(result.ExtraInEDS, page.EDS)
			edsPages = append(edsPages, page.EDS)
		case "sourceFetchFailures":
			result.SourceFetchFailures = append(result.SourceFetchFailures, page.Source)
		case "edsFetchFailures":
			result.EDSFetchFailures = append(result.EDSFetchFailures, page.EDS)
			edsPages = append(edsPages, page.EDS)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ComparisonResult{}, err
	}
	if err := rows.Close(); err != nil {
		return ComparisonResult{}, err
	}
	visuals, err := s.comparisonVisuals(id)
	if err != nil {
		return ComparisonResult{}, err
	}
	for i := range result.Matched {
		result.Matched[i].Visuals = visuals[result.Matched[i].Path]
		result.Matched[i] = NormalizeComparedPage(result.Matched[i])
	}
	for i := range result.UncertainMatches {
		result.UncertainMatches[i].Visuals = visuals[result.UncertainMatches[i].Path]
		result.UncertainMatches[i] = NormalizeComparedPage(result.UncertainMatches[i])
	}
	result.Blocks, result.Sections, _, _ = aggregate(edsPages)
	matchedForAggregation := append([]ComparedPage{}, result.Matched...)
	matchedForAggregation = append(matchedForAggregation, result.UncertainMatches...)
	result.Links, result.SEO = aggregateComparison(matchedForAggregation)
	return NormalizeComparisonResult(result), nil
}

func (s *SQLiteStore) comparisonVisuals(id string) (map[string][]VisualDiff, error) {
	rows, err := s.db.Query(`
SELECT page_key, viewport, source_image, eds_image, diff_image, diff_percent, status, COALESCE(error, '')
FROM comparison_visuals WHERE comparison_id = ? ORDER BY page_key, viewport`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	visuals := map[string][]VisualDiff{}
	for rows.Next() {
		var key string
		var visual VisualDiff
		if err := rows.Scan(&key, &visual.Viewport, &visual.SourceImage, &visual.EDSImage, &visual.DiffImage, &visual.DiffPercent, &visual.Status, &visual.Error); err != nil {
			return nil, err
		}
		visuals[key] = append(visuals[key], visual)
	}
	return visuals, rows.Err()
}

type scannerRows interface {
	Scan(dest ...any) error
}

func scanFromRows(row scannerRows) (ScanSummary, error) {
	var scan ScanSummary
	var startedAt, finishedAt string
	var performance, accessibility, bestPractices, seo, health sql.NullFloat64
	if err := row.Scan(&scan.ID, &scan.InputURL, &scan.RootURL, &scan.Status, &scan.Phase, &startedAt, &finishedAt,
		&scan.DiscoveredPages, &scan.CompletedPages, &scan.FailedPages,
		&scan.FastCompletedPages, &scan.AuditQueuedPages, &scan.AuditCompletedPages, &scan.AuditFailedPages,
		&performance, &accessibility, &bestPractices, &seo, &health, &scan.Error); err != nil {
		return scan, err
	}
	if scan.Phase == "" {
		scan.Phase = scan.Status
	}
	if scan.FastCompletedPages == 0 && scan.CompletedPages > 0 {
		scan.FastCompletedPages = scan.CompletedPages
	}
	scan.StartedAt = parseTime(startedAt)
	if finishedAt != "" {
		scan.FinishedAt = parseTime(finishedAt)
	}
	scan.Scores = ScoreSet{
		Performance:   fromNull(performance),
		Accessibility: fromNull(accessibility),
		BestPractices: fromNull(bestPractices),
		SEO:           fromNull(seo),
		Health:        fromNull(health),
	}
	return scan, nil
}

func comparisonFromRows(row scannerRows) (ComparisonSummary, error) {
	var comparison ComparisonSummary
	var startedAt, finishedAt, matchesUpdatedAt, sourceDiscoveryJSON, edsDiscoveryJSON string
	var fastReady int
	var score sql.NullFloat64
	if err := row.Scan(&comparison.ID, &comparison.SourceInputURL, &comparison.EDSInputURL, &comparison.SourceRootURL, &comparison.EDSRootURL,
		&comparison.Status, &comparison.Phase, &fastReady, &comparison.BackgroundPhase, &startedAt, &finishedAt,
		&comparison.SourcePages, &comparison.EDSPages, &comparison.SourceAnalyzed, &comparison.EDSAnalyzed, &matchesUpdatedAt,
		&comparison.MatchedPages, &comparison.UncertainMatches, &comparison.MissingInEDS, &comparison.ExtraInEDS, &comparison.SourceFetchFailures,
		&comparison.EDSFetchFailures, &comparison.MetadataDiffs, &comparison.LinkDiffs, &comparison.VisualQueued,
		&comparison.VisualCompleted, &comparison.VisualFailed, &comparison.VisualReview, &comparison.VisualFail,
		&comparison.LighthouseQueued, &comparison.LighthouseCompleted, &comparison.LighthouseFailed, &score,
		&sourceDiscoveryJSON, &edsDiscoveryJSON, &comparison.Error); err != nil {
		return comparison, err
	}
	if comparison.Phase == "" {
		comparison.Phase = comparison.Status
	}
	comparison.FastReady = fastReady != 0
	comparison.StartedAt = parseTime(startedAt)
	if finishedAt != "" {
		comparison.FinishedAt = parseTime(finishedAt)
	}
	if matchesUpdatedAt != "" {
		comparison.MatchesUpdatedAt = parseTime(matchesUpdatedAt)
	}
	if comparison.SourceAnalyzed == 0 && comparison.SourcePages > 0 {
		comparison.SourceAnalyzed = comparison.SourcePages
	}
	if comparison.EDSAnalyzed == 0 && comparison.EDSPages > 0 {
		comparison.EDSAnalyzed = comparison.EDSPages
	}
	comparison.MigrationScore = fromNull(score)
	_ = json.Unmarshal([]byte(sourceDiscoveryJSON), &comparison.SourceDiscovery)
	_ = json.Unmarshal([]byte(edsDiscoveryJSON), &comparison.EDSDiscovery)
	comparison.SourceDiscovery = NormalizeDiscoveryReport(comparison.SourceDiscovery)
	comparison.EDSDiscovery = NormalizeDiscoveryReport(comparison.EDSDiscovery)
	return comparison, nil
}

func comparedPageFromRows(rows *sql.Rows) (ComparedPage, string, error) {
	var page ComparedPage
	var group string
	var sourceJSON, edsJSON, fieldsJSON, linksJSON, issuesJSON, sourceAliasesJSON, edsAliasesJSON string
	if err := rows.Scan(&page.Path, &group, &page.Status, &page.Severity, &page.MatchType, &page.MatchConfidence,
		&page.MatchReason, &sourceAliasesJSON, &edsAliasesJSON, &sourceJSON, &edsJSON, &fieldsJSON, &linksJSON, &issuesJSON); err != nil {
		return page, group, err
	}
	_ = json.Unmarshal([]byte(sourceJSON), &page.Source)
	_ = json.Unmarshal([]byte(edsJSON), &page.EDS)
	_ = json.Unmarshal([]byte(fieldsJSON), &page.FieldDiffs)
	_ = json.Unmarshal([]byte(linksJSON), &page.LinkDiffs)
	_ = json.Unmarshal([]byte(issuesJSON), &page.Issues)
	_ = json.Unmarshal([]byte(sourceAliasesJSON), &page.SourceAliases)
	_ = json.Unmarshal([]byte(edsAliasesJSON), &page.EDSAliases)
	return NormalizeComparedPage(page), group, nil
}

func pageFromRows(rows *sql.Rows) (PageResult, error) {
	var page PageResult
	var ogJSON, linksJSON, blocksJSON, sectionsJSON string
	var performance, accessibility, bestPractices, seo, health sql.NullFloat64
	err := rows.Scan(&page.URL, &page.StatusCode, &page.Title, &page.H1, &page.Canonical, &page.Description,
		&page.Robots, &page.Lang, &ogJSON, &linksJSON, &blocksJSON, &sectionsJSON,
		&page.BlockCount, &page.SectionCount, &page.LinkCount, &page.InternalLinks, &page.ExternalLinks,
		&performance, &accessibility, &bestPractices, &seo, &health, &page.AuditStatus, &page.AuditError, &page.FetchError)
	if err != nil {
		return page, err
	}
	_ = json.Unmarshal([]byte(ogJSON), &page.OG)
	_ = json.Unmarshal([]byte(linksJSON), &page.Links)
	_ = json.Unmarshal([]byte(blocksJSON), &page.Blocks)
	_ = json.Unmarshal([]byte(sectionsJSON), &page.Sections)
	page.Lighthouse = ScoreSet{
		Performance:   fromNull(performance),
		Accessibility: fromNull(accessibility),
		BestPractices: fromNull(bestPractices),
		SEO:           fromNull(seo),
		Health:        fromNull(health),
	}
	return NormalizePage(page), nil
}

func aggregateComparison(matched []ComparedPage) (ComparisonLinks, ComparisonSEO) {
	var links ComparisonLinks
	var seo ComparisonSEO
	for _, page := range matched {
		links.SourceTotal += page.Source.LinkCount
		links.EDSTotal += page.EDS.LinkCount
		for _, diff := range page.LinkDiffs {
			switch diff.Field {
			case "missing internal links":
				links.MissingInternal += countCSV(diff.Source)
			case "added internal links":
				links.AddedInternal += countCSV(diff.EDS)
			case "missing external links":
				links.MissingExternal += countCSV(diff.Source)
			case "added external links":
				links.AddedExternal += countCSV(diff.EDS)
			case "missing assets":
				links.MissingAssets += countCSV(diff.Source)
			case "added assets":
				links.AddedAssets += countCSV(diff.EDS)
			}
		}
		for _, diff := range page.FieldDiffs {
			seo.MetadataDiffs++
			switch diff.Field {
			case "title":
				seo.TitleDiffs++
			case "h1":
				seo.H1Diffs++
			case "description":
				seo.DescriptionDiffs++
			case "og:title", "og:description", "og:image", "og:url", "og:type", "og:site_name":
				seo.OGDiffs++
			}
		}
	}
	links.MatchedPageDiffs = links.MissingInternal + links.AddedInternal + links.MissingExternal + links.AddedExternal + links.MissingAssets + links.AddedAssets
	return links, seo
}

func countCSV(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return len(strings.Split(value, ", "))
}

func aggregate(pages []PageResult) ([]BlockStat, []SectionStat, LinkStats, SEOStats) {
	blockMap := map[string]*BlockStat{}
	blockPages := map[string]map[string]bool{}
	sectionMap := map[string]*SectionStat{}
	sectionPages := map[string]map[string]bool{}
	internalUnique := map[string]bool{}
	externalUnique := map[string]bool{}
	assetUnique := map[string]bool{}
	var links LinkStats
	var seo SEOStats

	for _, page := range pages {
		if stringsTrim(page.Title) == "" {
			seo.MissingTitle++
		}
		if stringsTrim(page.Description) == "" {
			seo.MissingDescription++
		}
		if stringsTrim(page.H1) == "" {
			seo.MissingH1++
		}
		if stringsTrim(page.Canonical) == "" {
			seo.MissingCanonical++
		}
		if stringsTrim(page.OG.Title) == "" {
			seo.MissingOGTitle++
		}
		if stringsTrim(page.OG.Image) == "" {
			seo.MissingOGImage++
		}
		if stringsTrim(page.OG.URL) == "" {
			seo.MissingOGURL++
		}

		for _, block := range page.Blocks {
			stat := blockMap[block.Name]
			if stat == nil {
				stat = &BlockStat{Name: block.Name, Variations: map[string]int{}}
				blockMap[block.Name] = stat
				blockPages[block.Name] = map[string]bool{}
			}
			stat.Count++
			blockPages[block.Name][page.URL] = true
			for _, variation := range block.Variations {
				stat.Variations[variation]++
			}
		}
		for _, section := range page.Sections {
			for _, variation := range section.Variations {
				stat := sectionMap[variation]
				if stat == nil {
					stat = &SectionStat{Variation: variation}
					sectionMap[variation] = stat
					sectionPages[variation] = map[string]bool{}
				}
				stat.Count++
				sectionPages[variation][page.URL] = true
			}
		}
		for _, link := range page.Links {
			links.Total++
			switch link.Kind {
			case "internal":
				links.Internal++
				internalUnique[link.URL] = true
			case "external":
				links.External++
				externalUnique[link.URL] = true
			case "asset":
				links.Asset++
				assetUnique[link.URL] = true
			case "mail":
				links.Mail++
			case "tel":
				links.Tel++
			case "hash":
				links.Hash++
			}
		}
	}

	blocks := []BlockStat{}
	for name, stat := range blockMap {
		stat.Pages = sortedSet(blockPages[name])
		blocks = append(blocks, *stat)
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Count == blocks[j].Count {
			return blocks[i].Name < blocks[j].Name
		}
		return blocks[i].Count > blocks[j].Count
	})

	sections := []SectionStat{}
	for variation, stat := range sectionMap {
		stat.Pages = sortedSet(sectionPages[variation])
		sections = append(sections, *stat)
	}
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].Count == sections[j].Count {
			return sections[i].Variation < sections[j].Variation
		}
		return sections[i].Count > sections[j].Count
	})

	links.UniqueInternal = len(internalUnique)
	links.UniqueExternal = len(externalUnique)
	links.UniqueAsset = len(assetUnique)
	return blocks, sections, links, seo
}

func (s *SQLiteStore) recomputeStoredSummary(scan *ScanSummary) error {
	rows, err := s.db.Query(`
SELECT COALESCE(fetch_error, ''), COALESCE(audit_status, ''), COALESCE(audit_error, ''), health
FROM pages WHERE scan_id = ?`, scan.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	pages := []PageResult{}
	for rows.Next() {
		var page PageResult
		var health sql.NullFloat64
		if err := rows.Scan(&page.FetchError, &page.AuditStatus, &page.AuditError, &health); err != nil {
			return err
		}
		page.Lighthouse.Health = fromNull(health)
		pages = append(pages, NormalizePage(page))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	*scan = recomputeSummaryFromPages(*scan, pages)
	return nil
}

func recomputeSummaryFromPages(scan ScanSummary, pages []PageResult) ScanSummary {
	if len(pages) == 0 {
		return scan
	}
	scan.CompletedPages = len(pages)
	scan.FastCompletedPages = len(pages)
	scan.FailedPages = 0
	audited := 0
	auditCompleted := 0
	auditFailed := 0
	for _, page := range pages {
		page = NormalizePage(page)
		if page.FetchError != "" {
			scan.FailedPages++
		}
		if page.AuditStatus == "complete" || page.AuditStatus == "failed" || page.AuditStatus == "running" {
			audited++
		}
		if page.AuditStatus == "complete" {
			auditCompleted++
		}
		if page.AuditStatus == "failed" {
			auditFailed++
		}
	}
	if scan.AuditQueuedPages == 0 && audited > 0 {
		scan.AuditQueuedPages = audited
	}
	if scan.Status != "running" {
		scan.AuditCompletedPages = auditCompleted
		scan.AuditFailedPages = auditFailed
		return scan
	}
	if scan.AuditCompletedPages == 0 && auditCompleted > 0 {
		scan.AuditCompletedPages = auditCompleted
	}
	if scan.AuditFailedPages == 0 && auditFailed > 0 {
		scan.AuditFailedPages = auditFailed
	}
	return scan
}

func (s *SQLiteStore) ensureColumn(table, column, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if strings.EqualFold(name, column) {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

func nullable(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func fromNull(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	v := value.Float64
	return &v
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func sortedSet(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func stringsTrim(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\n' || value[0] == '\t' || value[0] == '\r') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\n' && last != '\t' && last != '\r' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}
