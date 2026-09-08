// Page rendering for the vision path (M12): when a PDF has no text layer
// (scanned document), the local multimodal model can still read it — the
// engine rasterizes the first pages and the UI attaches them to the chat
// request. Rendering needs MuPDF (cgo, bundled by go-fitz); images are
// JPEG-compressed and downscaled so a whole document fits the IPC budget.
package pdfdoc

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decode whatever MuPDF hands back

	"golang.org/x/image/draw"

	"github.com/gen2brain/go-fitz"
)

// RenderOptions tune the rasterization. Zero fields pick the defaults.
type RenderOptions struct {
	MaxPages int     // pages rendered at most (default 4)
	DPI      float64 // rasterization resolution (default 150)
	MaxWidth int     // widest edge in px before JPEG encoding (default 1400)
	Quality  int     // JPEG quality (default 70)
}

func (o RenderOptions) withDefaults() RenderOptions {
	if o.MaxPages <= 0 {
		o.MaxPages = 4
	}
	if o.DPI <= 0 {
		o.DPI = 150
	}
	if o.MaxWidth <= 0 {
		o.MaxWidth = 1400
	}
	if o.Quality <= 0 {
		o.Quality = 70
	}
	return o
}

// PageImage is one rendered page: index is 0-based, Data is JPEG bytes.
type PageImage struct {
	Index int
	MIME  string
	Data  []byte
}

// RenderPages rasterizes up to opts.MaxPages pages of a PDF. Extra pages are
// simply not returned — the caller sees fewer images than document pages and
// annotates the prompt accordingly.
func RenderPages(data []byte, opts RenderOptions) ([]PageImage, error) {
	opts = opts.withDefaults()
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-")) {
		return nil, fmt.Errorf("il file non è un PDF valido")
	}
	doc, err := fitz.NewFromMemory(data)
	if err != nil {
		return nil, fmt.Errorf("pdf non leggibile: %w", err)
	}
	defer doc.Close()

	total := doc.NumPage()
	if total == 0 {
		return nil, fmt.Errorf("pdf senza pagine")
	}
	n := min(opts.MaxPages, total)

	pages := make([]PageImage, 0, n)
	for i := range n {
		raw, err := doc.ImageDPI(i, opts.DPI)
		if err != nil {
			continue // one broken page must not kill the rest
		}
		jpegBytes, err := encodeJPEG(raw, opts.MaxWidth, opts.Quality)
		if err != nil {
			continue
		}
		pages = append(pages, PageImage{Index: i, MIME: "image/jpeg", Data: jpegBytes})
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("nessuna pagina renderizzabile")
	}
	return pages, nil
}

// encodeJPEG downscales to MaxWidth (keeping aspect) and JPEG-encodes.
func encodeJPEG(src *image.RGBA, maxWidth, quality int) ([]byte, error) {
	img := image.Image(src)
	if w := img.Bounds().Dx(); w > maxWidth {
		h := img.Bounds().Dy() * maxWidth / w
		dst := image.NewRGBA(image.Rect(0, 0, maxWidth, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
