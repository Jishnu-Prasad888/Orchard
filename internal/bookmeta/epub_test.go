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
