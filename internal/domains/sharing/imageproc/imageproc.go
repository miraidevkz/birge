// Package imageproc implements the sharing ImageProcessor port with the Go
// standard library (image, image/jpeg, image/png). It decodes JPEG or PNG,
// downscales so the long side is at most MaxPhotoDimension, and re-encodes as
// JPEG. Re-encoding drops EXIF and other metadata (ADR-0015).
package imageproc

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // register, then reject: we only accept JPEG/PNG at the boundary
	"image/jpeg"
	"image/png"
	"math"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
)

// MaxPixels bounds width*height before a full decode. The declared size in the
// header is checked first, so a small file that claims a huge image (a
// decompression bomb) is refused instead of allocating gigabytes.
const MaxPixels = 40_000_000

// Processor implements port.ImageProcessor.
type Processor struct{}

var _ port.ImageProcessor = (*Processor)(nil)

// Process decodes raw as JPEG or PNG, re-encodes as JPEG without metadata,
// and caps the long side at model.MaxPhotoDimension.
func (Processor) Process(_ context.Context, raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, model.ErrPhoto
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: decode config: %w", model.ErrPhoto, err)
	}
	switch format {
	case "jpeg", "png":
	default:
		return nil, fmt.Errorf("%w: unsupported format %s", model.ErrPhoto, format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, fmt.Errorf("%w: %dx%d exceeds the pixel limit", model.ErrPhoto, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: decode: %w", model.ErrPhoto, err)
	}
	out := downscale(img, model.MaxPhotoDimension)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("%w: encode: %w", model.ErrPhoto, err)
	}
	return buf.Bytes(), nil
}

func downscale(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	long := w
	if h > w {
		long = h
	}
	if long <= maxDim {
		// Copy onto a new NRGBA so any embedded metadata from the decoder
		// backing store is not retained (draw drops EXIF; jpeg.Encode writes
		// a clean JFIF stream).
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		return dst
	}
	scale := float64(maxDim) / float64(long)
	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	// Nearest-neighbour is enough for the MVP strip/resize; quality is not
	// the goal — removing metadata and capping size is.
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// Ensure png/jpeg codecs stay linked when tests only import this package.
var (
	_ = jpeg.Encode
	_ = png.Encode
)
