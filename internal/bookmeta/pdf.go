package bookmeta

import (
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// ExtractPDF reads the /Info dictionary (title/author) and the full plain
// text of a PDF file at path. PDF has no standard publisher/ISBN fields, so
// those are left empty; year comes from CreationDate when present. The
// cover is a rasterization of the first page (see renderPDFCover) — its
// failure never fails the whole extraction, since the title/author/text
// above are already independently useful.
func ExtractPDF(pdfPath string) (ex Extracted, err error) {
	// ledongthuc/pdf documents itself as incomplete and can panic on a
	// malformed file; one bad PDF must not take down the library's whole
	// background job worker.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic extracting pdf: %v", r)
		}
	}()

	f, r, openErr := pdf.Open(pdfPath)
	if openErr != nil {
		return Extracted{}, fmt.Errorf("open pdf: %w", openErr)
	}
	defer func() { _ = f.Close() }()

	info := r.Trailer().Key("Info")
	ex.Title = info.Key("Title").Text()
	ex.Author = info.Key("Author").Text()
	if cd := info.Key("CreationDate").Text(); cd != "" {
		ex.Year = yearFromPDFDate(cd)
	}

	if textReader, terr := r.GetPlainText(); terr == nil {
		if b, rerr := io.ReadAll(textReader); rerr == nil {
			ex.Text = string(b)
		}
	}

	ex.Cover, ex.CoverExt = renderPDFCover(pdfPath)

	return ex, nil
}

// yearFromPDFDate pulls the year out of a PDF date string, which looks like
// "D:20240115093000" (the "D:" prefix is optional per spec).
func yearFromPDFDate(s string) string {
	s = strings.TrimPrefix(s, "D:")
	if len(s) >= 4 {
		return s[:4]
	}
	return ""
}
