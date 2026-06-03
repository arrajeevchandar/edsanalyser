package scanner

import (
	"net/url"
	"path"
	"strings"
)

var excludedExtensions = map[string]bool{
	".7z": true, ".avif": true, ".bmp": true, ".css": true, ".csv": true, ".doc": true, ".docx": true,
	".eot": true, ".gif": true, ".gz": true, ".ico": true, ".jpeg": true, ".jpg": true, ".js": true,
	".json": false, ".map": true, ".mov": true, ".mp3": true, ".mp4": true, ".otf": true, ".pdf": true,
	".png": true, ".ppt": true, ".pptx": true, ".rar": true, ".svg": true, ".tar": true, ".ttf": true,
	".webm": true, ".webp": true, ".woff": true, ".woff2": true, ".xls": true, ".xlsx": true, ".zip": true,
}

func NormalizeInputURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errInvalidURL("URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errInvalidURL("URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, errInvalidURL("URL host is required")
	}
	parsed.Fragment = ""
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed, nil
}

func normalizePageURL(raw string, base *url.URL) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") || strings.HasPrefix(lower, "javascript:") {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(parsed)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", false
	}
	resolved.Fragment = ""
	resolved.Host = strings.ToLower(resolved.Host)
	if resolved.Path == "" {
		resolved.Path = "/"
	}
	if isExcludedAssetURL(resolved) {
		return "", false
	}
	return resolved.String(), true
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func isSameOriginURL(raw string, base *url.URL) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return sameOrigin(parsed, base)
}

func isExcludedAssetURL(u *url.URL) bool {
	ext := strings.ToLower(path.Ext(u.Path))
	return excludedExtensions[ext]
}

// mediaExtensions is the set of file extensions treated as media assets when the
// page source is swept for images, audio, and video files.
var mediaExtensions = map[string]bool{
	// images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true,
	".avif": true, ".bmp": true, ".ico": true, ".tif": true, ".tiff": true, ".heic": true,
	".heif": true, ".jfif": true, ".apng": true,
	// video
	".mp4": true, ".webm": true, ".mov": true, ".avi": true, ".mkv": true, ".ogv": true,
	".m4v": true, ".mpg": true, ".mpeg": true, ".wmv": true, ".flv": true, ".3gp": true,
	// audio
	".mp3": true, ".wav": true, ".ogg": true, ".oga": true, ".aac": true, ".flac": true,
	".m4a": true, ".weba": true, ".opus": true, ".wma": true, ".mid": true, ".midi": true,
}

// isMediaURL reports whether the URL path ends in a known media extension.
func isMediaURL(u *url.URL) bool {
	return mediaExtensions[strings.ToLower(path.Ext(u.Path))]
}

// assetFileName returns the (URL-decoded) file name of a media URL, e.g.
// "/media_1ab2.jpg" -> "media_1ab2.jpg".
func assetFileName(u *url.URL) string {
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(base); err == nil {
		base = decoded
	}
	return base
}

func classifyLink(raw string, resolved *url.URL, base *url.URL) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(lower, "mailto:"):
		return "mail"
	case strings.HasPrefix(lower, "tel:"):
		return "tel"
	case strings.HasPrefix(lower, "#"):
		return "hash"
	case isExcludedAssetURL(resolved):
		return "asset"
	case sameOrigin(resolved, base):
		return "internal"
	default:
		return "external"
	}
}

type errInvalidURL string

func (e errInvalidURL) Error() string {
	return string(e)
}
