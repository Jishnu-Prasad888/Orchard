package bookmeta

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestPDF writes a minimal, structurally real single-page PDF (proper
// object offsets and an xref table, so a spec-compliant reader can open it
// without a repair pass) with the given Info title/author and body text,
// and returns its path.
func buildTestPDF(t *testing.T, title, author, body string) string {
	t.Helper()
	var buf bytes.Buffer
	offsets := make([]int, 7)
	buf.WriteString("%PDF-1.4\n")
	write := func(n int, s string) {
		offsets[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, s)
	}
	write(1, "<< /Type /Catalog /Pages 2 0 R >>")
	write(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	write(3, "<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 4 0 R >> >> "+
		"/MediaBox [0 0 200 200] /Contents 5 0 R >>")
	write(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	content := fmt.Sprintf("BT /F1 24 Tf 10 100 Td (%s) Tj ET", body)
	write(5, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	write(6, fmt.Sprintf("<< /Title (%s) /Author (%s) /CreationDate (D:20210615093000) >>", title, author))

	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 7\n0000000000 65535 f \n")
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Size 7 /Root 1 0 R /Info 6 0 R >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOffset)
	buf.WriteString("%%EOF")

	path := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// buildTestPDFWithContent is buildTestPDF generalized to an arbitrary raw
// content stream, for cover-rendering tests that need to control exactly
// what is (or is not) drawn on the page.
func buildTestPDFWithContent(t *testing.T, content string) string {
	t.Helper()
	var buf bytes.Buffer
	offsets := make([]int, 6)
	buf.WriteString("%PDF-1.4\n")
	write := func(n int, s string) {
		offsets[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, s)
	}
	write(1, "<< /Type /Catalog /Pages 2 0 R >>")
	write(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	write(3, "<< /Type /Page /Parent 2 0 R /Resources << >> /MediaBox [0 0 200 200] /Contents 4 0 R >>")
	write(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))

	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 5\n0000000000 65535 f \n")
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOffset)
	buf.WriteString("%%EOF")

	path := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenderPDFCover_FilledPageProducesAJPEGCover(t *testing.T) {
	// A solid black rectangle covering the whole page.
	path := buildTestPDFWithContent(t, "0 0 0 rg 0 0 200 200 re f")

	data, ext := renderPDFCover(path)
	if ext != ".jpg" {
		t.Fatalf("ext = %q, want .jpg", ext)
	}
	if len(data) == 0 {
		t.Fatal("got no cover bytes")
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Errorf("cover bytes do not decode as JPEG: %v", err)
	}
}

func TestRenderPDFCover_TextPageProducesACover(t *testing.T) {
	// Text set in a standard, non-embedded font: the case a partial
	// renderer gets wrong, and most of what a real title page is.
	path := buildTestPDF(t, "Test Book", "Jane Doe", "Hello World")

	data, ext := renderPDFCover(path)
	if ext != ".jpg" || len(data) == 0 {
		t.Fatalf("got (%d bytes, %q), want a JPEG cover", len(data), ext)
	}
}

func TestRenderPDFCover_BlankPageYieldsNoCover(t *testing.T) {
	path := buildTestPDFWithContent(t, "")

	data, ext := renderPDFCover(path)
	if data != nil || ext != "" {
		t.Errorf("got (%d bytes, %q) for a blank page, want no cover", len(data), ext)
	}
}

func TestRenderPDFCover_MalformedPDFDoesNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pdf")
	if err := os.WriteFile(path, []byte("not a pdf at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, ext := renderPDFCover(path)
	if data != nil || ext != "" {
		t.Errorf("got (%d bytes, %q) for a malformed file, want no cover", len(data), ext)
	}
}

func TestExtractPDF(t *testing.T) {
	path := buildTestPDF(t, "Test Book", "Jane Doe", "Hello World")

	ex, err := ExtractPDF(path)
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	if ex.Title != "Test Book" {
		t.Errorf("Title = %q, want %q", ex.Title, "Test Book")
	}
	if ex.Author != "Jane Doe" {
		t.Errorf("Author = %q, want %q", ex.Author, "Jane Doe")
	}
	if ex.Year != "2021" {
		t.Errorf("Year = %q, want %q", ex.Year, "2021")
	}
	if !strings.Contains(ex.Text, "Hello World") {
		t.Errorf("Text = %q, missing expected content", ex.Text)
	}
}

func TestExtractPDF_NotAPDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a.pdf")
	if err := os.WriteFile(path, []byte("plain text, not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractPDF(path); err == nil {
		t.Error("expected an error extracting a non-PDF file, got nil")
	}
}

func TestYearFromPDFDate(t *testing.T) {
	cases := map[string]string{
		"D:20210615093000": "2021",
		"20210615093000":   "2021",
		"D:21":             "",
		"":                 "",
	}
	for in, want := range cases {
		if got := yearFromPDFDate(in); got != want {
			t.Errorf("yearFromPDFDate(%q) = %q, want %q", in, got, want)
		}
	}
}
