package blob

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	_ "image/gif" // decoders, registered for image.Decode
	"image/jpeg"
	_ "image/png"
	"os"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/nebuloss/cafard/internal/dto"
)

// Variant sizes (PLAN section 8).
const (
	AgentLongEdge = 1568
	ThumbLongEdge = 320
	jpegQuality   = 85
)

type variants struct {
	width, height  int // of the original, orientation applied
	agent, thumb   []byte
	agentW, agentH int
	thumbW, thumbH int
}

// derive decodes an image file and encodes its variants, in a pool slot:
// never on more goroutines than the pool allows.
func (b *Store) derive(ctx context.Context, path string) (variants, error) {
	select {
	case b.pool <- struct{}{}:
	case <-ctx.Done():
		return variants{}, ctx.Err()
	}
	type result struct {
		v   variants
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-b.pool }()
		v, err := deriveFile(path, b.cfg.MaxMegapixels)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-ctx.Done():
		return variants{}, ctx.Err()
	}
}

func deriveFile(path string, maxMP int) (variants, error) {
	f, err := os.Open(path)
	if err != nil {
		return variants{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return variants{}, dto.Errorf(dto.CodeBadRequest, "An image could not be read: %v.", err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > int64(maxMP)*1_000_000 {
		return variants{}, dto.Errorf(dto.CodeTooLarge, "An image is %dx%d pixels; the limit is %d megapixels.", cfg.Width, cfg.Height, maxMP)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return variants{}, err
	}
	head := make([]byte, 128<<10)
	n, _ := f.ReadAt(head, 0)
	orientation := exifOrientation(head[:n])
	img, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return variants{}, dto.Errorf(dto.CodeBadRequest, "An image could not be decoded: %v.", err)
	}
	return makeVariants(img, orientation)
}

func makeVariants(img image.Image, orientation int) (variants, error) {
	var v variants
	agent := orient(fit(img, AgentLongEdge, draw.CatmullRom), orientation)
	thumb := fit(agent, ThumbLongEdge, draw.ApproxBiLinear)
	v.width, v.height = img.Bounds().Dx(), img.Bounds().Dy()
	if orientation >= 5 {
		v.width, v.height = v.height, v.width
	}
	var err error
	if v.agent, err = encodeJPEG(agent); err != nil {
		return v, err
	}
	if v.thumb, err = encodeJPEG(thumb); err != nil {
		return v, err
	}
	v.agentW, v.agentH = agent.Bounds().Dx(), agent.Bounds().Dy()
	v.thumbW, v.thumbH = thumb.Bounds().Dx(), thumb.Bounds().Dy()
	return v, nil
}

// fit scales img so its long edge is at most edge, onto an opaque white
// RGBA (JPEG has no alpha). A smaller image is only copied.
func fit(img image.Image, edge int, scaler draw.Scaler) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > edge || h > edge {
		if w >= h {
			h, w = max(1, h*edge/w), edge
		} else {
			w, h = max(1, w*edge/h), edge
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	stddraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	if w == b.Dx() && h == b.Dy() {
		stddraw.Draw(dst, dst.Bounds(), img, b.Min, stddraw.Over)
		return dst
	}
	scaler.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encoding a variant: %w", err)
	}
	return buf.Bytes(), nil
}

// orient applies an EXIF orientation (1 to 8) to src.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			si := src.PixOffset(x, y)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// exifOrientation reads the orientation tag of a JPEG's EXIF block; 1
// (as stored) when there is none.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xff || b[1] != 0xd8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xff {
			return 1
		}
		marker := b[i+1]
		if marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 {
			return 1 // start of scan: no EXIF before the image data
		}
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		if size < 2 || i+2+size > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+size]
		if marker == 0xe1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := range n {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			v := int(bo.Uint16(t[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
