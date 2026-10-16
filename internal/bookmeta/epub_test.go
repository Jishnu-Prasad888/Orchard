package bookmeta

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestEPUB writes a minimal but structurally real EPUB (container.xml
// + OPF package with metadata/manifest/spine + one XHTML chapter + a fake
// cover image) to a temp file and returns its path.
func buildTestEPUB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>The Great Test</dc:title>
    <dc:creator>Jane Author</dc:creator>
    <dc:publisher>Test Press</dc:publisher>
    <dc:date>2021-06-15</dc:date>
    <dc:description>A book about tests.</dc:description>
    <dc:identifier opf:scheme="ISBN">978-3-16-148410-0</dc:identifier>
  </metadata>
  <manifest>
    <item id="cover-img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
    <item id="chap1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="chap1"/>
  </spine>
</package>`,
		"OEBPS/chapter1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><style>body { color: red; }</style></head>
<body>
<h1>Chapter One</h1>
<p>It was a dark and stormy night.</p>
<script>alert("should not appear");</script>
</body>
</html>`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	coverW, err := zw.Create("OEBPS/cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	fakeCover := []byte("\xff\xd8\xff\xe0fake-jpeg-bytes")
	if _, err := coverW.Write(fakeCover); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractEPUB(t *testing.T) {
	path := buildTestEPUB(t)

	ex, err := ExtractEPUB(path)
	if err != nil {
		t.Fatalf("ExtractEPUB: %v", err)
	}

	if ex.Title != "The Great Test" {
		t.Errorf("Title = %q, want %q", ex.Title, "The Great Test")
	}
	if ex.Author != "Jane Author" {
		t.Errorf("Author = %q, want %q", ex.Author, "Jane Author")
	}
	if ex.Publisher != "Test Press" {
		t.Errorf("Publisher = %q, want %q", ex.Publisher, "Test Press")
	}
	if ex.Year != "2021" {
		t.Errorf("Year = %q, want %q", ex.Year, "2021")
	}
	if ex.ISBN != "978-3-16-148410-0" {
		t.Errorf("ISBN = %q, want %q", ex.ISBN, "978-3-16-148410-0")
	}
	if ex.Description != "A book about tests." {
		t.Errorf("Description = %q, want %q", ex.Description, "A book about tests.")
	}
	if ex.CoverExt != ".jpg" {
		t.Errorf("CoverExt = %q, want .jpg", ex.CoverExt)
	}
	if string(ex.Cover) != "\xff\xd8\xff\xe0fake-jpeg-bytes" {
		t.Errorf("Cover bytes did not round-trip, got %q", ex.Cover)
	}
	if !strings.Contains(ex.Text, "It was a dark and stormy night.") {
		t.Errorf("Text = %q, missing expected chapter content", ex.Text)
	}
	if !strings.Contains(ex.Text, "Chapter One") {
		t.Errorf("Text = %q, missing heading", ex.Text)
	}
	if strings.Contains(ex.Text, "should not appear") {
		t.Errorf("Text = %q, script content leaked into extracted text", ex.Text)
	}
	if strings.Contains(ex.Text, "color: red") {
		t.Errorf("Text = %q, style content leaked into extracted text", ex.Text)
	}
}

func TestExtractEPUB_NotAZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-an-epub.epub")
	if err := os.WriteFile(path, []byte("plain text, not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractEPUB(path); err == nil {
		t.Error("expected an error extracting a non-zip file, got nil")
	}
}

func TestEpubISBN_FallsBackToIdentifierShape(t *testing.T) {
	ids := []epubIdentifier{{Scheme: "", Value: "urn:isbn:9783161484100"}}
	if got := epubISBN(ids); got != "9783161484100" {
		t.Errorf("epubISBN = %q, want %q", got, "9783161484100")
	}
}

func TestYearFromDateString(t *testing.T) {
	cases := map[string]string{
		"2021-06-15": "2021",
		"2021":       "2021",
		"":           "",
		"not a date": "",
	}
	for in, want := range cases {
		if got := yearFromDateString(in); got != want {
			t.Errorf("yearFromDateString(%q) = %q, want %q", in, got, want)
		}
	}
}

// writeTestEPUB zips the given archive members into an EPUB at a temp path.
func writeTestEPUB(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

const testEPUBContainer = `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

// fakeJPEG is enough of a JPEG for content sniffing to call it one.
const fakeJPEG = "\xff\xd8\xff\xe0\x00\x10JFIF\x00 cover"

// TestExtractEPUB_CoverFallbacks covers books that do not declare a cover
// the way the EPUB 3 spec asks, each of which used to come out coverless.
func TestExtractEPUB_CoverFallbacks(t *testing.T) {
	opf := func(metadata, manifest, tail string) string {
		return `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>T</dc:title>` + metadata + `</metadata>
  <manifest>` + manifest + `<item id="chap1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="chap1"/></spine>` + tail + `
</package>`
	}
	const chapter = `<html><body><p>Text.</p></body></html>`

	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "EPUB 2 meta naming the manifest item",
			files: map[string]string{
				"OEBPS/content.opf": opf(`<meta name="cover" content="img1"/>`,
					`<item id="img1" href="images/front.jpg" media-type="image/jpeg"/>`, ""),
				"OEBPS/images/front.jpg": fakeJPEG,
				"OEBPS/chapter1.xhtml":   chapter,
			},
		},
		{
			name: "meta naming the image file instead of its id",
			files: map[string]string{
				"OEBPS/content.opf": opf(`<meta name="cover" content="images/front.jpg"/>`,
					`<item id="img1" href="images/front.jpg" media-type="image/jpeg"/>`, ""),
				"OEBPS/images/front.jpg": fakeJPEG,
				"OEBPS/chapter1.xhtml":   chapter,
			},
		},
		{
			name: "undeclared image called cover, percent-encoded href",
			files: map[string]string{
				"OEBPS/content.opf": opf("",
					`<item id="x1" href="images/Book%20Cover.jpg" media-type="image/jpeg"/>`, ""),
				"OEBPS/images/Book Cover.jpg": fakeJPEG,
				"OEBPS/chapter1.xhtml":        chapter,
			},
		},
		{
			name: "guide cover page showing an svg image",
			files: map[string]string{
				"OEBPS/content.opf": opf("",
					`<item id="pg" href="text/titlepage.xhtml" media-type="application/xhtml+xml"/>`+
						`<item id="x1" href="images/a.jpg" media-type="image/jpeg"/>`,
					`<guide><reference type="cover" href="text/titlepage.xhtml#top"/></guide>`),
				"OEBPS/text/titlepage.xhtml": `<html><body><svg><image width="10" xlink:href="../images/a.jpg"/></svg></body></html>`,
				"OEBPS/images/a.jpg":         fakeJPEG,
				"OEBPS/chapter1.xhtml":       chapter,
			},
		},
		{
			name: "first page of the book showing an image",
			files: map[string]string{
				"OEBPS/content.opf":    opf("", `<item id="x1" href="pic.jpg" media-type="image/jpeg"/>`, ""),
				"OEBPS/pic.jpg":        fakeJPEG,
				"OEBPS/chapter1.xhtml": `<html><body><img alt="" src="pic.jpg"/></body></html>`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.files["META-INF/container.xml"] = testEPUBContainer
			ex, err := ExtractEPUB(writeTestEPUB(t, tc.files))
			if err != nil {
				t.Fatalf("ExtractEPUB: %v", err)
			}
			if string(ex.Cover) != fakeJPEG || ex.CoverExt != ".jpg" {
				t.Errorf("cover = (%d bytes, %q), want the book's image as .jpg", len(ex.Cover), ex.CoverExt)
			}
		})
	}
}

func TestExtractEPUB_NoImagesMeansNoCover(t *testing.T) {
	ex, err := ExtractEPUB(writeTestEPUB(t, map[string]string{
		"META-INF/container.xml": testEPUBContainer,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>T</dc:title></metadata>
  <manifest><item id="chap1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="chap1"/></spine>
</package>`,
		"OEBPS/chapter1.xhtml": `<html><body><p>Text only.</p></body></html>`,
	}))
	if err != nil {
		t.Fatalf("ExtractEPUB: %v", err)
	}
	if len(ex.Cover) != 0 || ex.CoverExt != "" {
		t.Errorf("cover = (%d bytes, %q), want none", len(ex.Cover), ex.CoverExt)
	}
}
