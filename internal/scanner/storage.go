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
