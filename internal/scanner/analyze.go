package scanner

import (
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

func AnalyzeHTML(pageURL string, body io.Reader, root *url.URL) (PageResult, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return PageResult{}, err
	}
	pageBase, err := url.Parse(pageURL)
	if err != nil {
		pageBase = root
	}

	page := PageResult{URL: pageURL}
	page.ScriptCount = countElements(doc, "script")
	page.Title = textOfFirst(doc, "title")
	page.H1 = textOfFirst(doc, "h1")
	page.Canonical = firstLinkRel(doc, "canonical")
	page.Description = firstMeta(doc, "name", "description")
	page.Robots = firstMeta(doc, "name", "robots")
	page.Lang = attr(findFirst(doc, "html"), "lang")
	page.OG = OpenGraph{
		Title:       firstMeta(doc, "property", "og:title"),
		Description: firstMeta(doc, "property", "og:description"),
		Image:       firstMeta(doc, "property", "og:image"),
		URL:         firstMeta(doc, "property", "og:url"),
		Type:        firstMeta(doc, "property", "og:type"),
		SiteName:    firstMeta(doc, "property", "og:site_name"),
	}

	page.Links = extractLinks(doc, pageBase, root)
	for _, link := range page.Links {
		switch link.Kind {
		case "internal":
			page.InternalLinks++
		case "external":
			page.ExternalLinks++
		}
	}
	page.LinkCount = len(page.Links)

	page.Sections, page.Blocks = extractEDS(doc)
	page.SectionCount = len(page.Sections)
	page.BlockCount = len(page.Blocks)
	return NormalizePage(page), nil
}

func countElements(doc *html.Node, tag string) int {
	count := 0
	walk(doc, func(n *html.Node) {
		if isElement(n, tag) {
			count++
		}
	})
	return count
}

func extractEDS(doc *html.Node) ([]SectionInfo, []BlockInfo) {
	main := findFirst(doc, "main")
	if main == nil {
		return []SectionInfo{}, []BlockInfo{}
	}

	sections := []SectionInfo{}
	blocks := []BlockInfo{}
	sectionIndex := 0
	for child := main.FirstChild; child != nil; child = child.NextSibling {
		if !isElement(child, "div") {
			continue
		}
		sectionIndex++
		section := SectionInfo{Index: sectionIndex, Variations: []string{}, Blocks: []string{}}
		variations := make(map[string]bool)
		for _, className := range classList(child) {
			if className != "section" {
				variations[className] = true
			}
		}

		for blockNode := child.FirstChild; blockNode != nil; blockNode = blockNode.NextSibling {
			if !isElement(blockNode, "div") {
				continue
			}
			classes := classList(blockNode)
			if isSectionMetadata(classes) {
				for _, variation := range metadataVariations(blockNode) {
					variations[variation] = true
				}
				continue
			}
			if len(classes) == 0 {
				continue
			}
			name := classes[0]
			blockVariations := []string{}
			for _, variation := range classes[1:] {
				if variation != "" && variation != "block" {
					blockVariations = append(blockVariations, variation)
				}
			}
			sort.Strings(blockVariations)
			blocks = append(blocks, BlockInfo{Name: name, Variations: blockVariations, SectionIndex: sectionIndex})
			section.Blocks = append(section.Blocks, name)
		}

		for variation := range variations {
			section.Variations = append(section.Variations, variation)
		}
		sort.Strings(section.Variations)
		sections = append(sections, section)
	}
	return sections, blocks
}

func metadataVariations(n *html.Node) []string {
	var values []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if isElement(node, "div") {
			texts := directChildTexts(node)
			if len(texts) >= 2 {
				key := normalizeToken(texts[0])
				if key == "style" || key == "styles" || key == "class" || key == "classes" {
					for _, value := range strings.Fields(strings.ReplaceAll(texts[1], ",", " ")) {
						values = append(values, normalizeToken(value))
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return values
}

func directChildTexts(n *html.Node) []string {
	var texts []string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			value := strings.TrimSpace(child.Data)
			if value != "" {
				texts = append(texts, value)
			}
			continue
		}
		if child.Type == html.ElementNode {
			value := strings.TrimSpace(nodeText(child))
			if value != "" {
				texts = append(texts, value)
			}
		}
	}
	return texts
}

func extractLinks(doc *html.Node, pageBase *url.URL, root *url.URL) []LinkInfo {
	links := []LinkInfo{}
	// Asset URLs are de-duplicated on their path (ignoring the query string) so a
	// single responsive image rendered as a <picture> with many <source> variants
	// counts once, not once per width/format candidate.
	assetSeen := map[string]bool{}

	// resolve turns a raw reference into an absolute http(s) URL with the fragment
	// removed, skipping inline data:/blob:/javascript: references.
	resolve := func(raw string) (*url.URL, bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, false
		}
		lower := strings.ToLower(raw)
		if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "javascript:") {
			return nil, false
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, false
		}
		resolved := pageBase.ResolveReference(parsed)
		resolved.Fragment = ""
		return resolved, true
	}

	// addAsset records a media file once per page, keyed on its path (query dropped),
	// using the file name as its display name.
	addAsset := func(raw string, resolved *url.URL, name string) {
		clean := *resolved
		clean.RawQuery = ""
		key := clean.String()
		if assetSeen[key] {
			return
		}
		assetSeen[key] = true
		if name == "" {
			name = assetFileName(&clean)
		}
		links = append(links, LinkInfo{
			Href: strings.TrimSpace(raw),
			URL:  key,
			Text: name,
			Kind: "asset",
		})
	}

	// considerMedia adds raw as an asset when it points at a known media file.
	considerMedia := func(raw string) {
		resolved, ok := resolve(raw)
		if !ok || (resolved.Scheme != "http" && resolved.Scheme != "https") {
			return
		}
		if isMediaURL(resolved) {
			addAsset(raw, resolved, "")
		}
	}

	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}

		// Anchors: navigation, plus any explicitly linked downloadable files.
		if isElement(n, "a") {
			href := strings.TrimSpace(attr(n, "href"))
			if href == "" {
				return
			}
			resolved, ok := resolve(href)
			if !ok {
				return
			}
			kind := classifyLink(href, resolved, root)
			if kind == "asset" {
				addAsset(href, resolved, "")
				return
			}
			links = append(links, LinkInfo{
				Href:     href,
				URL:      resolved.String(),
				Text:     compactText(nodeText(n)),
				Target:   attr(n, "target"),
				Rel:      attr(n, "rel"),
				Kind:     kind,
				External: kind == "external",
			})
			return
		}

		// Sweep every URL-bearing attribute on any element for media files so that
		// images, audio, video, <source>/<link> refs, lazy-loaded data-src, and CSS
		// background images are all captured by extension.
		considerMedia(attr(n, "src"))
		considerMedia(attr(n, "href"))
		considerMedia(attr(n, "poster"))
		considerMedia(attr(n, "data-src"))
		for _, u := range srcsetURLs(attr(n, "srcset")) {
			considerMedia(u)
		}
		for _, u := range srcsetURLs(attr(n, "data-srcset")) {
			considerMedia(u)
		}
		for _, u := range cssURLs(attr(n, "style")) {
			considerMedia(u)
		}
	})

	return links
}

// srcsetURLs pulls the URL out of each candidate in a srcset attribute, ignoring
// the width ("750w") or density ("2x") descriptor that follows it.
func srcsetURLs(srcset string) []string {
	srcset = strings.TrimSpace(srcset)
	if srcset == "" {
		return nil
	}
	urls := []string{}
	for _, candidate := range strings.Split(srcset, ",") {
		fields := strings.Fields(candidate)
		if len(fields) == 0 {
			continue
		}
		urls = append(urls, fields[0])
	}
	return urls
}

var cssURLPattern = regexp.MustCompile(`url\(\s*['"]?([^'")]+)['"]?\s*\)`)

// cssURLs extracts the targets of url(...) references from an inline style value,
// catching CSS background images and similar media.
func cssURLs(style string) []string {
	if strings.TrimSpace(style) == "" {
		return nil
	}
	matches := cssURLPattern.FindAllStringSubmatch(style, -1)
	urls := make([]string, 0, len(matches))
	for _, match := range matches {
		urls = append(urls, strings.TrimSpace(match[1]))
	}
	return urls
}

func firstMeta(doc *html.Node, keyAttr, keyValue string) string {
	keyValue = strings.ToLower(keyValue)
	var value string
	walk(doc, func(n *html.Node) {
		if value != "" || !isElement(n, "meta") {
			return
		}
		if strings.EqualFold(attr(n, keyAttr), keyValue) {
			value = attr(n, "content")
		}
	})
	return strings.TrimSpace(value)
}

func firstLinkRel(doc *html.Node, rel string) string {
	var value string
	walk(doc, func(n *html.Node) {
		if value != "" || !isElement(n, "link") {
			return
		}
		if strings.EqualFold(attr(n, "rel"), rel) {
			value = attr(n, "href")
		}
	})
	return strings.TrimSpace(value)
}

func textOfFirst(doc *html.Node, tag string) string {
	node := findFirst(doc, tag)
	if node == nil {
		return ""
	}
	return compactText(nodeText(node))
}

func findFirst(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if isElement(n, tag) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirst(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func walk(n *html.Node, fn func(*html.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walk(child, fn)
	}
}

func isElement(n *html.Node, tag string) bool {
	return n != nil && n.Type == html.ElementNode && strings.EqualFold(n.Data, tag)
}

func attr(n *html.Node, name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func classList(n *html.Node) []string {
	raw := attr(n, "class")
	fields := strings.Fields(raw)
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if normalized := normalizeToken(field); normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}

func isSectionMetadata(classes []string) bool {
	if len(classes) == 0 {
		return false
	}
	first := classes[0]
	return first == "section-metadata" || first == "metadata"
}

func normalizeToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.Trim(value, ".#")
	return value
}

func nodeText(n *html.Node) string {
	var builder strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
			builder.WriteString(" ")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(n)
	return builder.String()
}

func compactText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
