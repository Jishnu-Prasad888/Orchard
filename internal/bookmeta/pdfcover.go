package bookmeta

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// PDF covers are rendered by PDFium — the PDF engine Chromium ships — so a
// cover comes out for every PDF a browser can open, not just the ones a
// partial renderer happens to understand. It runs as a WebAssembly module
// embedded in this binary and executed by a pure-Go runtime: no cgo, no
// shared library, nothing to install, and the same code cross-compiles to
// every platform Orchard is released for.

const (
	// pdfCoverMaxWidth and pdfCoverMaxHeight bound the rendered cover. The
	// page keeps its aspect ratio inside this box; it is sized for a sharp
	// grid thumbnail on a high-density screen, not for reading.
	pdfCoverMaxWidth  = 600
	pdfCoverMaxHeight = 900

	// pdfCoverCandidatePages is how many leading pages are tried for a
	// cover. A scan or an export sometimes starts with a blank sheet; the
	// first page with something on it is the cover.
	pdfCoverCandidatePages = 3

	// pdfCoverRenderTimeout bounds one file's cover render. Each
	// library has a single job worker, so a PDF that sends the renderer
	// spinning must cost that library one cover, not its whole queue.
	pdfCoverRenderTimeout = 30 * time.Second

	// minCoverContentFraction is the share of sampled pixels that must be
	// non-white for a rendered page to count as a cover rather than a blank
	// sheet.
	minCoverContentFraction = 0.005
)

var (
	pdfiumOnce    sync.Once
	pdfiumPool    pdfium.Pool
	pdfiumInitErr error
	pdfiumCache   *rendererCache
)

// pdfiumRenderers returns the process-wide PDFium pool, compiling the
// embedded module on first use — a server that never indexes a PDF never
// pays for it. The compiled code is reused from the on-disk cache when one
// is set and verifies (see pdfcache.go).
func pdfiumRenderers() (pdfium.Pool, error) {
	pdfiumOnce.Do(func() {
		pdfiumPool, pdfiumCache, pdfiumInitErr = startPDFium(rendererCacheBase, rendererCacheLogger)
	})
	return pdfiumPool, pdfiumInitErr
}

// startPDFium starts a renderer pool using the cache under cacheBase (""
// for none). If wazero refuses the cache, it is wiped and the pool starts
// without it: a bad cache must never cost a cover.
func startPDFium(cacheBase string, logger *slog.Logger) (pdfium.Pool, *rendererCache, error) {
	cache := prepareRendererCache(cacheBase, logger)
	pool, err := newPDFiumPool(cache)
	if err != nil && cache != nil {
		logger.Warn("pdf renderer rejected its cache; compiling afresh", "dir", cache.dir, "error", err)
		cache.discard()
		cache = nil
		pool, err = newPDFiumPool(nil)
	}
	return pool, cache, err
}

func newPDFiumPool(cache *rendererCache) (pdfium.Pool, error) {
	// The bundled module needs WebAssembly exception handling (go-pdfium's
	// default config enables it; a custom one must too).
	cfg := wazero.NewRuntimeConfig().WithCoreFeatures(
		api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling)
	if cache != nil {
		compiled, err := wazero.NewCompilationCacheWithDir(cache.dir)
		if err != nil {
			return nil, err
		}
		cfg = cfg.WithCompilationCache(compiled)
	}
	return webassembly.Init(webassembly.Config{
		MinIdle:       0,
		MaxIdle:       1,
		MaxTotal:      2,
		RuntimeConfig: cfg,
	})
}

// renderPDFCover rasterizes the first non-blank leading page of the PDF at
// pdfPath as a JPEG cover. A PDF that cannot be rendered just has no cover.
func renderPDFCover(pdfPath string) (data []byte, ext string) {
	img, err := renderPDFCoverImage(pdfPath)
	if err != nil || img == nil {
		return nil, ""
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, ""
	}
	return buf.Bytes(), ".jpg"
}

// renderPDFCoverImage is renderPDFCover without the encoding step. A nil
// image with a nil error means the PDF opened but its leading pages are
// blank.
func renderPDFCoverImage(pdfPath string) (image.Image, error) {
	pool, err := pdfiumRenderers()
	if err != nil {
		return nil, fmt.Errorf("start pdf renderer: %w", err)
	}
	instance, err := pool.GetInstance(pdfCoverRenderTimeout)
	if err != nil {
		return nil, fmt.Errorf("get pdf renderer: %w", err)
	}

	type result struct {
		img image.Image
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{err: fmt.Errorf("pdf renderer panicked: %v", r)}
			}
		}()
		img, err := renderLeadingPage(instance, pdfPath)
		done <- result{img: img, err: err}
	}()

	select {
	case res := <-done:
		_ = instance.Close()
		// The renderer ran (whatever this particular PDF did) and the
		// process survived it: the compiled code is good to keep.
		pdfiumCache.confirmed()
		return res.img, res.err
	case <-time.After(pdfCoverRenderTimeout):
		// Killing the instance tears down its WebAssembly module, which is
		// what actually stops a render that will never finish.
		_ = instance.Kill()
		return nil, errors.New("pdf cover render timed out")
	}
}

func renderLeadingPage(instance pdfium.Pdfium, pdfPath string) (image.Image, error) {
	f, err := os.Open(pdfPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	// Read through the open file rather than loading it whole: a scanned
	// book can run to hundreds of megabytes, and a cover needs one page.
	doc, err := instance.OpenDocument(&requests.OpenDocument{FileReader: f, FileReaderSize: info.Size()})
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer func() { _, _ = instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document}) }()

	count, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, fmt.Errorf("count pdf pages: %w", err)
	}

	var firstErr error
	for i := 0; i < count.PageCount && i < pdfCoverCandidatePages; i++ {
		render, err := instance.RenderPageInPixels(&requests.RenderPageInPixels{
			Page: requests.Page{
				ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i},
			},
			Width:  pdfCoverMaxWidth,
			Height: pdfCoverMaxHeight,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("render pdf page %d: %w", i+1, err)
			}
			continue
		}
		// The rendered pixels live in the renderer's memory until Cleanup;
		// copy them out so the image outlives it.
		rendered, ok := render.Result.RenderedImage.(*image.RGBA)
		if !ok {
			render.Cleanup()
			continue
		}
		img := cloneRGBA(rendered)
		render.Cleanup()
		if hasVisibleContent(img) {
			return img, nil
		}
	}
	return nil, firstErr
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	for y := 0; y < src.Rect.Dy(); y++ {
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+4*src.Rect.Dx()], src.Pix[y*src.Stride:y*src.Stride+4*src.Rect.Dx()])
	}
	return dst
}

// hasVisibleContent reports whether at least minCoverContentFraction of a
// sparse pixel sample is non-white — see minCoverContentFraction.
func hasVisibleContent(img image.Image) bool {
	b := img.Bounds()
	const stride = 3 // every 3rd pixel in each dimension: fast, plenty for this check
	var total, nonWhite int
	for y := b.Min.Y; y < b.Max.Y; y += stride {
		for x := b.Min.X; x < b.Max.X; x += stride {
			rr, g, bb, _ := img.At(x, y).RGBA()
			total++
			if rr < 0xf000 || g < 0xf000 || bb < 0xf000 {
				nonWhite++
			}
		}
	}
	if total == 0 {
		return false
	}
	return float64(nonWhite)/float64(total) >= minCoverContentFraction
}
