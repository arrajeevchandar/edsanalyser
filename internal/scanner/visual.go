package scanner

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const DefaultVisualDiffDir = ".data/visual-diffs"

type VisualViewport struct {
	Name   string
	Width  int
	Height int
}

var DefaultVisualViewports = []VisualViewport{
	{Name: "desktop", Width: 1440, Height: 900},
	{Name: "mobile", Width: 390, Height: 844},
}

type VisualRunner interface {
	Diff(ctx context.Context, comparisonID string, pageKey string, sourceURL string, edsURL string, viewport VisualViewport) VisualDiff
}

type ChromeVisualRunner struct {
	OutputDir string
	Timeout   time.Duration
	Chrome    string
}

func NewChromeVisualRunner() ChromeVisualRunner {
	return ChromeVisualRunner{OutputDir: DefaultVisualDiffDir, Timeout: 45 * time.Second}
}

func (r ChromeVisualRunner) Diff(ctx context.Context, comparisonID string, pageKey string, sourceURL string, edsURL string, viewport VisualViewport) VisualDiff {
	visual := VisualDiff{Viewport: viewport.Name, Status: "error"}
	if r.OutputDir == "" {
		r.OutputDir = DefaultVisualDiffDir
	}
	if r.Timeout <= 0 {
		r.Timeout = 45 * time.Second
	}
	chrome, err := r.chromePath()
	if err != nil {
		visual.Error = err.Error()
		return visual
	}

	slug := visualSlug(pageKey)
	dir := filepath.Join(r.OutputDir, comparisonID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		visual.Error = err.Error()
		return visual
	}
	sourceFile := filepath.Join(dir, slug+"-"+viewport.Name+"-source.png")
	edsFile := filepath.Join(dir, slug+"-"+viewport.Name+"-eds.png")
	diffFile := filepath.Join(dir, slug+"-"+viewport.Name+"-diff.png")

	// Capture both screenshots concurrently — each launches its own headless
	// browser process, so running them in parallel roughly halves the per-page time.
	var wg sync.WaitGroup
	var sourceErr, edsErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		sourceErr = r.capture(ctx, chrome, sourceURL, sourceFile, viewport)
	}()
	go func() {
		defer wg.Done()
		edsErr = r.capture(ctx, chrome, edsURL, edsFile, viewport)
	}()
	wg.Wait()
	if sourceErr != nil {
		visual.Error = "legacy screenshot failed: " + sourceErr.Error()
		return visual
	}
	if edsErr != nil {
		visual.Error = "EDS screenshot failed: " + edsErr.Error()
		return visual
	}
	percent, err := writeImageDiff(sourceFile, edsFile, diffFile)
	if err != nil {
		visual.Error = err.Error()
		return visual
	}

	visual.SourceImage = comparisonVisualURL(comparisonID, filepath.Base(sourceFile))
	visual.EDSImage = comparisonVisualURL(comparisonID, filepath.Base(edsFile))
	visual.DiffImage = comparisonVisualURL(comparisonID, filepath.Base(diffFile))
	visual.DiffPercent = percent
	visual.Status = classifyVisualDiff(percent)
	return visual
}

func (r ChromeVisualRunner) chromePath() (string, error) {
	if r.Chrome != "" {
		return r.Chrome, nil
	}
	// Chromium-based browsers all support the same headless screenshot flags, so
	// Microsoft Edge (present on every modern Windows install) is a first-class
	// fallback when Google Chrome is not installed.
	candidates := []string{"chrome", "google-chrome", "chromium", "chromium-browser", "msedge", "microsoft-edge"}
	var explicit []string
	switch runtime.GOOS {
	case "windows":
		candidates = append([]string{"chrome.exe", "msedge.exe"}, candidates...)
		explicit = []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
	case "darwin":
		explicit = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	}
	for _, path := range explicit {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no Chromium-based browser (Chrome or Edge) was found for screenshots")
}

func (r ChromeVisualRunner) capture(ctx context.Context, chrome string, pageURL string, output string, viewport VisualViewport) error {
	captureCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	// A dedicated, throwaway profile per capture is essential: without it, a
	// headless launch hands off to any already-running Chrome (e.g. the user's
	// open browser) and exits 0 without taking the screenshot, or blocks on the
	// profile lock until the deadline. It also lets captures run concurrently.
	profile, err := os.MkdirTemp("", "eds-shot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)

	// Resolve an absolute output path so it never depends on Chrome's CWD.
	target := output
	if abs, absErr := filepath.Abs(output); absErr == nil {
		target = abs
	}

	cmd := exec.CommandContext(captureCtx, chrome,
		"--headless=new",
		"--disable-gpu",
		"--hide-scrollbars",
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-dev-shm-usage",
		"--user-data-dir="+profile,
		fmt.Sprintf("--window-size=%d,%d", viewport.Width, viewport.Height),
		"--virtual-time-budget=8000",
		"--screenshot="+target,
		pageURL,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		if captureCtx.Err() != nil {
			return captureCtx.Err()
		}
		return fmt.Errorf("chrome screenshot failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Chrome can exit 0 without producing a file; treat that as a failure here so
	// the diff step reports a clear cause instead of a downstream "file not found".
	if info, statErr := os.Stat(output); statErr != nil || info.Size() == 0 {
		return fmt.Errorf("chrome exited without writing a screenshot")
	}
	return nil
}

func writeImageDiff(sourceFile string, edsFile string, diffFile string) (float64, error) {
	source, err := readPNG(sourceFile)
	if err != nil {
		return 0, err
	}
	eds, err := readPNG(edsFile)
	if err != nil {
		return 0, err
	}
	width := maxInt(source.Bounds().Dx(), eds.Bounds().Dx())
	height := maxInt(source.Bounds().Dy(), eds.Bounds().Dy())
	if width == 0 || height == 0 {
		return 0, fmt.Errorf("empty screenshot")
	}
	diff := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(diff, diff.Bounds(), image.NewUniform(color.RGBA{R: 248, G: 248, B: 248, A: 255}), image.Point{}, draw.Src)

	var changed int
	total := width * height
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			a, okA := pixel(source, x, y)
			b, okB := pixel(eds, x, y)
			if !okA || !okB || colorDistance(a, b) > 48 {
				changed++
				diff.SetRGBA(x, y, color.RGBA{R: 250, G: 15, B: 0, A: 255})
			} else {
				gray := uint8((uint16(a.R) + uint16(a.G) + uint16(a.B)) / 3)
				diff.SetRGBA(x, y, color.RGBA{R: gray, G: gray, B: gray, A: 65})
			}
		}
	}
	file, err := os.Create(diffFile)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if err := png.Encode(file, diff); err != nil {
		return 0, err
	}
	return math.Round((float64(changed)/float64(total))*10000) / 100, nil
}

func readPNG(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return png.Decode(file)
}

func pixel(img image.Image, x int, y int) (color.RGBA, bool) {
	if !image.Pt(x, y).In(img.Bounds()) {
		return color.RGBA{}, false
	}
	r, g, b, a := img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y).RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}, true
}

func colorDistance(a color.RGBA, b color.RGBA) int {
	return absInt(int(a.R)-int(b.R)) + absInt(int(a.G)-int(b.G)) + absInt(int(a.B)-int(b.B)) + absInt(int(a.A)-int(b.A))
}

func classifyVisualDiff(percent float64) string {
	switch {
	case percent < 5:
		return "pass"
	case percent <= 20:
		return "review"
	default:
		return "fail"
	}
}

func visualSlug(key string) string {
	key = strings.Trim(key, "/")
	if key == "" {
		key = "home"
	}
	key = strings.ToLower(key)
	replacer := strings.NewReplacer("/", "-", "\\", "-", "?", "-", "&", "-", "=", "-", ":", "-", "*", "-", "\"", "-", "<", "-", ">", "-", "|", "-")
	key = replacer.Replace(key)
	key = strings.Trim(key, "-")
	if key == "" {
		return "page"
	}
	if len(key) > 90 {
		key = key[:90]
	}
	return key
}

func comparisonVisualURL(comparisonID string, fileName string) string {
	return "/api/comparisons/" + url.PathEscape(comparisonID) + "/visuals/" + url.PathEscape(fileName)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
