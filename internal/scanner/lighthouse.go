package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type LighthouseRunner interface {
	Audit(ctx context.Context, pageURL string) (ScoreSet, error)
}

type CLILighthouseRunner struct {
	Timeout time.Duration
	TempDir string
}

func NewCLILighthouseRunner(timeout time.Duration) CLILighthouseRunner {
	return CLILighthouseRunner{Timeout: timeout}
}

func (r CLILighthouseRunner) Audit(ctx context.Context, pageURL string) (ScoreSet, error) {
	if r.Timeout <= 0 {
		r.Timeout = 90 * time.Second
	}
	tempDir := r.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(".data", "lighthouse-temp")
	}
	absoluteTempDir, err := filepath.Abs(tempDir)
	if err != nil {
		absoluteTempDir = tempDir
	}
	if err := os.MkdirAll(absoluteTempDir, 0o755); err != nil {
		return ScoreSet{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	binary := "npx"
	if runtime.GOOS == "windows" {
		binary = "npx.cmd"
	}
	cmd := exec.CommandContext(ctx, binary,
		"lighthouse",
		pageURL,
		"--output=json",
		"--quiet",
		"--chrome-flags=--headless=new --no-sandbox",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = lighthouseEnv(absoluteTempDir)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ScoreSet{}, ctx.Err()
		}
		// Lighthouse writes the full JSON report to stdout before chrome-launcher
		// tears down its temporary Chrome profile. On Windows that cleanup can
		// fail with EPERM (the Chrome subprocess has not released its file
		// handles yet), making the CLI exit non-zero even though the audit
		// itself succeeded. Recover the report in that case instead of throwing
		// away a complete set of scores over a cosmetic cleanup error.
		if scores, parseErr := ParseLighthouseScores(stdout.Bytes()); parseErr == nil && scores.HasAnyScore() {
			return scores, nil
		}
		return ScoreSet{}, fmt.Errorf("lighthouse failed: %w: %s", err, stderr.String())
	}
	return ParseLighthouseScores(stdout.Bytes())
}

func lighthouseEnv(tempDir string) []string {
	env := os.Environ()
	seen := map[string]bool{}
	sanitized := make([]string, 0, len(env)+5)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		normalized := strings.ToUpper(key)
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		sanitized = append(sanitized, entry)
	}
	sanitized = append(sanitized,
		"TEMP="+tempDir,
		"TMP="+tempDir,
		"TMPDIR="+tempDir,
		"CHROME_CONFIG_HOME="+filepath.Join(tempDir, "chrome-config"),
		"XDG_CACHE_HOME="+filepath.Join(tempDir, "cache"),
	)
	return sanitized
}

func ParseLighthouseScores(body []byte) (ScoreSet, error) {
	var report struct {
		Categories map[string]struct {
			Score *float64 `json:"score"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		return ScoreSet{}, err
	}

	score := ScoreSet{
		Performance:   scoreFrom(report.Categories["performance"].Score),
		Accessibility: scoreFrom(report.Categories["accessibility"].Score),
		BestPractices: scoreFrom(report.Categories["best-practices"].Score),
		SEO:           scoreFrom(report.Categories["seo"].Score),
	}
	score.Health = averagePointers(score.Performance, score.Accessibility, score.BestPractices, score.SEO)
	return score, nil
}

func scoreFrom(value *float64) *float64 {
	if value == nil {
		return nil
	}
	score := *value * 100
	return &score
}

func averagePointers(values ...*float64) *float64 {
	var total float64
	var count int
	for _, value := range values {
		if value == nil {
			continue
		}
		total += *value
		count++
	}
	if count == 0 {
		return nil
	}
	average := total / float64(count)
	return &average
}

type NoopLighthouseRunner struct{}

func (NoopLighthouseRunner) Audit(context.Context, string) (ScoreSet, error) {
	return ScoreSet{}, nil
}
