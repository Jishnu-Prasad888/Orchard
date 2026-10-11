package bookmeta

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"html"
	"io"
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

	if coverID := epubCoverItemID(pkg.Metadata.Meta, byID); coverID != "" {
		if item, ok := byID[coverID]; ok {
			coverPath := path.Join(opfDir, item.Href)
			if data, err := readZipFile(&zr.Reader, coverPath); err == nil {
				ex.Cover = data
				ex.CoverExt = extFromMediaType(item.MediaType)
			}
		}
	}

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

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "/")
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("not found in archive: %s", name)
}

func firstNonEmpty(vals []string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// epubCoverItemID resolves the manifest item ID for the cover image, trying
// the EPUB 3 way (manifest item with properties="cover-image") and falling
// back to the EPUB 2 way (<meta name="cover" content="{id}"/>).
func epubCoverItemID(metas []epubMeta, byID map[string]epubManifestItem) string {
	for id, item := range byID {
		if strings.Contains(item.Properties, "cover-image") {
			return id
		}
	}
	for _, m := range metas {
		if m.Name == "cover" && m.Content != "" {
			return m.Content
		}
	}
	return ""
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
