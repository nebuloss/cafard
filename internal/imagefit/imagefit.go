// Package imagefit makes an image the size a model is shown, before an
// agent's read_file returns it (docs/design.md, "Decisions that come from the
// reader being a model": images are fitted before they are returned; the
// original is kept for people).
//
// The long edge is brought down to MaxEdge. The figure is claudication's
// measurement of the upstream (internal/relay/passes/images.go there): the
// API itself downscales every image to a 1568 px long edge on models before
// Claude 4.7, and to 2576 px on 4.7 and later. So 1568 loses nothing on the
// first and little on the second, and it bounds what a phone's 12-megapixel
// photo costs a context: about (1568 × 1176) / 750, some 2,500 tokens, where
// the original would be refused outright at more than 5 MB.
//
// A PNG stays a PNG, because a screenshot's text survives it where JPEG
// blurs it; anything else becomes a JPEG at quality 85.
package imagefit

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif" // decode only
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode only; a WebP leaves as JPEG.
)

const (
	// MaxEdge is the longest side, in pixels, an image is shown at.
	MaxEdge = 1568
	// MaxBytes is the largest image passed on as it is: under it, and within
	// MaxEdge, an image is not re-encoded at all.
	MaxBytes = 1 << 20
	// maxInput bounds what is decoded: a file larger than this is a camera
	// RAW or a mistake, and decoding it would cost more memory than the
	// containers cafard runs in have.
	maxInput = 40 << 20
)

// ErrNotAnImage is a file that does not decode as one.
var ErrNotAnImage = errors.New("not an image cafard can read: JPEG, PNG, GIF or WebP")

// Fit reads an image of type mediaType and returns it fitted, with its type.
// One already small enough comes back byte for byte.
func Fit(r io.Reader, mediaType string) ([]byte, string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > maxInput {
		return nil, "", errors.New("the image is larger than 40 MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", ErrNotAnImage
	}
	if longEdge(cfg.Width, cfg.Height) <= MaxEdge && len(raw) <= MaxBytes && (format == "jpeg" || format == "png" || format == "gif") {
		return raw, "image/" + format, nil
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", ErrNotAnImage
	}
	img = scale(img)
	var out bytes.Buffer
	if format == "png" {
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&out, img); err != nil {
			return nil, "", err
		}
		// A photograph saved as PNG can still be large once fitted; a JPEG
		// of it is a fraction of the size.
		if out.Len() <= MaxBytes {
			return out.Bytes(), "image/png", nil
		}
		out.Reset()
	}
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, "", err
	}
	return out.Bytes(), "image/jpeg", nil
}

func longEdge(w, h int) int {
	if w > h {
		return w
	}
	return h
}

// scale shrinks img so its long edge is MaxEdge, or returns it as it is.
func scale(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	edge := longEdge(w, h)
	if edge <= MaxEdge {
		return img
	}
	nw, nh := w*MaxEdge/edge, h*MaxEdge/edge
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}
