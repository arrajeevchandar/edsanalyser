package scanner

import "time"

type ScoreSet struct {
	Performance   *float64 `json:"performance"`
	Accessibility *float64 `json:"accessibility"`
	BestPractices *float64 `json:"bestPractices"`
	SEO           *float64 `json:"seo"`
	Health        *float64 `json:"health"`
}

// HasAnyScore reports whether at least one Lighthouse category produced a
// score. It is used to tell a usable (if partial) report apart from an empty
// one when Lighthouse exits non-zero.
func (s ScoreSet) HasAnyScore() bool {
	return s.Performance != nil || s.Accessibility != nil ||
		s.BestPractices != nil || s.SEO != nil || s.Health != nil
}

type ScanSummary struct {
	ID                  string    `json:"id"`
	InputURL            string    `json:"inputUrl"`
	RootURL             string    `json:"rootUrl"`
	Status              string    `json:"status"`
	Phase               string    `json:"phase"`
	StartedAt           time.Time `json:"startedAt"`
	FinishedAt          time.Time `json:"finishedAt,omitempty"`
	DiscoveredPages     int       `json:"discoveredPages"`
	CompletedPages      int       `json:"completedPages"`
	FailedPages         int       `json:"failedPages"`
	FastCompletedPages  int       `json:"fastCompletedPages"`
	AuditQueuedPages    int       `json:"auditQueuedPages"`
	AuditCompletedPages int       `json:"auditCompletedPages"`
	AuditFailedPages    int       `json:"auditFailedPages"`
	Scores              ScoreSet  `json:"scores"`
	Error               string    `json:"error,omitempty"`
}

type ScanResult struct {
	Summary     ScanSummary   `json:"summary"`
	Pages       []PageResult  `json:"pages"`
	Blocks      []BlockStat   `json:"blocks"`
	Sections    []SectionStat `json:"sections"`
	Links       LinkStats     `json:"links"`
	SEO         SEOStats      `json:"seo"`
	GeneratedAt time.Time     `json:"generatedAt"`
}

type PageResult struct {
	URL           string        `json:"url"`
	StatusCode    int           `json:"statusCode"`
	Title         string        `json:"title"`
	H1            string        `json:"h1"`
	Canonical     string        `json:"canonical"`
	Description   string        `json:"description"`
	Robots        string        `json:"robots"`
	Lang          string        `json:"lang"`
	OG            OpenGraph     `json:"og"`
	Links         []LinkInfo    `json:"links"`
	Blocks        []BlockInfo   `json:"blocks"`
	Sections      []SectionInfo `json:"sections"`
	BlockCount    int           `json:"blockCount"`
	SectionCount  int           `json:"sectionCount"`
	LinkCount     int           `json:"linkCount"`
	InternalLinks int           `json:"internalLinks"`
	ExternalLinks int           `json:"externalLinks"`
	Lighthouse    ScoreSet      `json:"lighthouse"`
	AuditStatus   string        `json:"auditStatus"`
	AuditError    string        `json:"auditError,omitempty"`
	FetchError    string        `json:"fetchError,omitempty"`
}

type OpenGraph struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	URL         string `json:"url"`
	Type        string `json:"type"`
	SiteName    string `json:"siteName"`
}

type LinkInfo struct {
	Href     string `json:"href"`
	URL      string `json:"url"`
	Text     string `json:"text"`
	Target   string `json:"target"`
	Rel      string `json:"rel"`
	Kind     string `json:"kind"`
	Status   int    `json:"status,omitempty"`
	PageURL  string `json:"pageUrl,omitempty"`
	External bool   `json:"external"`
}

type BlockInfo struct {
	Name         string   `json:"name"`
	Variations   []string `json:"variations"`
	SectionIndex int      `json:"sectionIndex"`
}

type SectionInfo struct {
	Index      int      `json:"index"`
	Variations []string `json:"variations"`
	Blocks     []string `json:"blocks"`
}

type BlockStat struct {
	Name       string         `json:"name"`
	Count      int            `json:"count"`
	Variations map[string]int `json:"variations"`
	Pages      []string       `json:"pages"`
}

type SectionStat struct {
	Variation string   `json:"variation"`
	Count     int      `json:"count"`
	Pages     []string `json:"pages"`
}

type LinkStats struct {
	Total          int `json:"total"`
	Internal       int `json:"internal"`
	External       int `json:"external"`
	Asset          int `json:"asset"`
	Mail           int `json:"mail"`
	Tel            int `json:"tel"`
	Hash           int `json:"hash"`
	UniqueInternal int `json:"uniqueInternal"`
	UniqueExternal int `json:"uniqueExternal"`
	UniqueAsset    int `json:"uniqueAsset"`
}

type SEOStats struct {
	MissingTitle       int `json:"missingTitle"`
	MissingDescription int `json:"missingDescription"`
	MissingH1          int `json:"missingH1"`
	MissingCanonical   int `json:"missingCanonical"`
	MissingOGTitle     int `json:"missingOgTitle"`
	MissingOGImage     int `json:"missingOgImage"`
	MissingOGURL       int `json:"missingOgUrl"`
}

type Event struct {
	Type      string    `json:"type"`
	ScanID    string    `json:"scanId"`
	Message   string    `json:"message,omitempty"`
	PageURL   string    `json:"pageUrl,omitempty"`
	Data      any       `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
