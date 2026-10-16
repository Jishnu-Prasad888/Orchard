package bookmeta

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Extracted is what an extractor derives from a single document file.
// Fields it cannot derive are left zero; the caller decides fallbacks.
type Extracted struct {
	Title       string
	Author      string
	Publisher   string
	Year        string
	ISBN        string
	Description string
	// Cover is the raw cover image bytes, if one was found.
	Cover    []byte
	CoverExt string // e.g. ".jpg", including the dot
	// Text is the extracted plain-text content, for Phase 6's full-text search.
	Text string
}

// epubContainer is META-INF/container.xml: it points at the OPF package
// document, whose path is not fixed by the EPUB spec.
type epubContainer struct {
	RootFiles struct {
		RootFile []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfile"`
	} `xml:"rootfiles"`
}

type epubIdentifier struct {
	Scheme string `xml:"scheme,attr"`
	Value  string `xml:",chardata"`
}

type epubMeta struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type epubManifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

// epubGuideRef is an EPUB 2 <guide> entry, pointing at a structural page
// of the book by role ("cover", "toc", …).
type epubGuideRef struct {
	Type string `xml:"type,attr"`
	Href string `xml:"href,attr"`
}

type epubItemRef struct {
	IDRef string `xml:"idref,attr"`
}

// epubPackage is the OPF package document (EPUB 2 and 3 compatible: the
// fields this cares about are unchanged between versions).
type epubPackage struct {
	Metadata struct {
		Title       []string         `xml:"title"`
		Creator     []string         `xml:"creator"`
		Publisher   []string         `xml:"publisher"`
		Date        []string         `xml:"date"`
		Description []string         `xml:"description"`
		Identifier  []epubIdentifier `xml:"identifier"`
		Meta        []epubMeta       `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []epubManifestItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		ItemRefs []epubItemRef `xml:"itemref"`
	} `xml:"spine"`
	Guide struct {
		References []epubGuideRef `xml:"reference"`
	} `xml:"guide"`
}

// ExtractEPUB reads title/author/publisher/year/ISBN/description, the cover
// image, and the concatenated plain text of every spine document from an
// EPUB file at path. An EPUB is just a zip archive, so this is hand-rolled
// against the OPF/container XML rather than pulling in a dedicated library.
func ExtractEPUB(epubPath string) (Extracted, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return Extracted{}, fmt.Errorf("open epub as zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	containerBytes, err := readZipFile(&zr.Reader, "META-INF/container.xml")
	if err != nil {
		return Extracted{}, fmt.Errorf("read container.xml: %w", err)
	}
	var container epubContainer
	if err := xml.Unmarshal(containerBytes, &container); err != nil {
		return Extracted{}, fmt.Errorf("parse container.xml: %w", err)
	}
	if len(container.RootFiles.RootFile) == 0 || container.RootFiles.RootFile[0].FullPath == "" {
		return Extracted{}, fmt.Errorf("container.xml has no rootfile")
	}
	opfPath := container.RootFiles.RootFile[0].FullPath
	opfDir := path.Dir(opfPath)

	opfBytes, err := readZipFile(&zr.Reader, opfPath)
	if err != nil {
		return Extracted{}, fmt.Errorf("read opf %s: %w", opfPath, err)
	}
	var pkg epubPackage
	if err := xml.Unmarshal(opfBytes, &pkg); err != nil {
		return Extracted{}, fmt.Errorf("parse opf: %w", err)
	}

	var ex Extracted
	ex.Title = firstNonEmpty(pkg.Metadata.Title)
	ex.Author = firstNonEmpty(pkg.Metadata.Creator)
	ex.Publisher = firstNonEmpty(pkg.Metadata.Publisher)
	ex.Description = firstNonEmpty(pkg.Metadata.Description)
	if d := firstNonEmpty(pkg.Metadata.Date); d != "" {
		ex.Year = yearFromDateString(d)
	}
	ex.ISBN = epubISBN(pkg.Metadata.Identifier)

	byID := make(map[string]epubManifestItem, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		byID[item.ID] = item
	}

	ex.Cover, ex.CoverExt = epubCover(&zr.Reader, opfDir, &pkg, byID)

	var text strings.Builder
	for _, ref := range pkg.Spine.ItemRefs {
		item, ok := byID[ref.IDRef]
		if !ok {
			continue
		}
		docPath := path.Join(opfDir, item.Href)
		data, err := readZipFile(&zr.Reader, docPath)
		if err != nil {
			continue
		}
		if text.Len() > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(plainTextFromHTML(data))
	}
	ex.Text = text.String()

	return ex, nil
}

// readZipFile reads one archive member by path. EPUBs in the wild get the
// details wrong often enough — a manifest href that is percent-encoded, or
// whose case differs from the archive entry — that an exact match is tried
// first and those two near-misses after it.
func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "/")
	candidates := []string{name}
	if unescaped, err := url.PathUnescape(name); err == nil && unescaped != name {
		candidates = append(candidates, unescaped)
	}
	find := func(match func(entry, want string) bool) *zip.File {
		for _, want := range candidates {
			for _, f := range zr.File {
				if match(f.Name, want) {
					return f
				}
			}
		}
		return nil
	}
	f := find(func(entry, want string) bool { return entry == want })
	if f == nil {
		f = find(strings.EqualFold)
	}
	if f == nil {
		return nil, fmt.Errorf("not found in archive: %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func firstNonEmpty(vals []string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// epubCover finds the book's cover image. Only some EPUBs declare one the
// way either spec asks, so this works down from the declared answers to
// educated guesses, taking the first that yields a readable image:
//
//  1. EPUB 3: the manifest item with properties="cover-image".
//  2. EPUB 2: <meta name="cover" content="…"/>, whose content is meant to
//     be a manifest item ID but is sometimes the image's href instead.
//  3. A manifest image whose ID or filename says "cover".
//  4. The <guide>'s cover page, or failing that the first page of the book:
//     the first image that page shows.
//  5. The first image in the manifest.
func epubCover(zr *zip.Reader, opfDir string, pkg *epubPackage, byID map[string]epubManifestItem) ([]byte, string) {
	items := pkg.Manifest.Items
	read := func(item epubManifestItem) ([]byte, string) {
		if item.Href == "" {
			return nil, ""
		}
		return readEPUBImage(zr, path.Join(opfDir, item.Href), item.MediaType)
	}

	for _, item := range items {
		if hasToken(item.Properties, "cover-image") {
			if data, ext := read(item); data != nil {
				return data, ext
			}
		}
	}

	for _, m := range pkg.Metadata.Meta {
		if !strings.EqualFold(m.Name, "cover") || m.Content == "" {
			continue
		}
		if item, ok := byID[m.Content]; ok {
			if data, ext := read(item); data != nil {
				return data, ext
			}
		}
		for _, item := range items {
			if item.Href == m.Content {
				if data, ext := read(item); data != nil {
					return data, ext
				}
			}
		}
	}

	for _, item := range items {
		if !isImageItem(item) {
			continue
		}
		if strings.Contains(strings.ToLower(item.ID), "cover") ||
			strings.Contains(strings.ToLower(path.Base(item.Href)), "cover") {
			if data, ext := read(item); data != nil {
				return data, ext
			}
		}
	}

	var pages []string
	for _, ref := range pkg.Guide.References {
		if strings.EqualFold(ref.Type, "cover") && ref.Href != "" {
			pages = append(pages, path.Join(opfDir, stripFragment(ref.Href)))
		}
	}
	if len(pkg.Spine.ItemRefs) > 0 {
		if item, ok := byID[pkg.Spine.ItemRefs[0].IDRef]; ok && item.Href != "" {
			pages = append(pages, path.Join(opfDir, item.Href))
		}
	}
	for _, page := range pages {
		doc, err := readZipFile(zr, page)
		if err != nil {
			continue
		}
		if m := pageImageRe.FindSubmatch(doc); m != nil {
			src := html.UnescapeString(string(m[1]))
			if strings.Contains(src, ":") { // a data: or remote URL, not a file in the book
				continue
			}
			if data, ext := readEPUBImage(zr, path.Join(path.Dir(page), stripFragment(src)), ""); data != nil {
				return data, ext
			}
		}
	}

	for _, item := range items {
		if isImageItem(item) {
			if data, ext := read(item); data != nil {
				return data, ext
			}
		}
	}
	return nil, ""
}

// pageImageRe matches the first image an XHTML page shows: an <img src>,
// or the <image href> / <image xlink:href> of an inline SVG, which is how
// many cover pages scale their picture to the screen.
var pageImageRe = regexp.MustCompile(`(?is)<(?:img|image)\b[^>]*?\b(?:src|xlink:href|href)\s*=\s*["']([^"']+)["']`)

// readEPUBImage reads an image out of the archive and names its type, from
// the declared media type when there is one and the bytes themselves when
// not. Anything that is not an image Orchard can serve yields nothing.
func readEPUBImage(zr *zip.Reader, name, mediaType string) ([]byte, string) {
	data, err := readZipFile(zr, name)
	if err != nil || len(data) == 0 {
		return nil, ""
	}
	ext := extFromMediaType(mediaType)
	if ext == "" {
		ext = extFromMediaType(http.DetectContentType(data))
	}
	if ext == "" {
		return nil, ""
	}
	return data, ext
}

func isImageItem(item epubManifestItem) bool {
	return extFromMediaType(item.MediaType) != ""
}

func hasToken(list, token string) bool {
	for _, t := range strings.Fields(list) {
		if t == token {
			return true
		}
	}
	return false
}

func stripFragment(href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		return href[:i]
	}
	return href
}

func extFromMediaType(mt string) string {
	switch strings.ToLower(mt) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

// yearFromDateString pulls the first 4-digit year out of an EPUB dc:date
// value, which may be a bare year or a full ISO date.
func yearFromDateString(s string) string {
	return yearRe.FindString(s)
}

var yearRe = regexp.MustCompile(`\d{4}`)

// epubISBN picks the most ISBN-like dc:identifier: one explicitly scheme'd
// as ISBN, else one whose value looks like an ISBN, else none.
func epubISBN(ids []epubIdentifier) string {
	for _, id := range ids {
		if strings.EqualFold(id.Scheme, "ISBN") {
			return cleanISBN(id.Value)
		}
	}
	for _, id := range ids {
		if looksLikeISBN(id.Value) {
			return cleanISBN(id.Value)
		}
	}
	return ""
}

func cleanISBN(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.ToLower(s), "urn:isbn:")
	return strings.TrimSpace(s)
}

var isbnDigitsRe = regexp.MustCompile(`^[0-9Xx-]{9,17}$`)

func looksLikeISBN(s string) bool {
	s = cleanISBN(s)
	digits := strings.ReplaceAll(strings.ReplaceAll(s, "-", ""), " ", "")
	return isbnDigitsRe.MatchString(s) && (len(digits) == 10 || len(digits) == 13)
}

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	whitespaceRe  = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLinesRe  = regexp.MustCompile(`\n{3,}`)
)

// plainTextFromHTML is a best-effort HTML-to-text conversion: good enough
// for full-text search content, not meant to preserve layout or structure.
func plainTextFromHTML(doc []byte) string {
	s := scriptStyleRe.ReplaceAllString(string(doc), " ")
	s = tagRe.ReplaceAllString(s, "\n")
	s = html.UnescapeString(s)
	s = whitespaceRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	s = strings.Join(out, "\n")
	return blankLinesRe.ReplaceAllString(s, "\n\n")
}
