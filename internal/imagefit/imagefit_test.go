package imagefit

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func picture(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Through Pix, not Set: twelve megapixels by Set take seconds under -race.
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			copy(row[4*x:], []byte{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	return img
}

func encode(t *testing.T, img image.Image, format string) []byte {
	t.Helper()
	var b bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&b, img)
	} else {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSmallImagesPassAsTheyAre(t *testing.T) {
	raw := encode(t, picture(320, 240), "jpeg")
	out, mt, err := Fit(bytes.NewReader(raw), "image/jpeg")
	if err != nil || mt != "image/jpeg" || !bytes.Equal(out, raw) {
		t.Errorf("a small JPEG was changed: %v %s", err, mt)
	}
}

func TestLargeImagesAreFittedToTheLongEdge(t *testing.T) {
	for _, format := range []string{"jpeg", "png"} {
		raw := encode(t, picture(4032, 3024), format)
		out, mt, err := Fit(bytes.NewReader(raw), "image/"+format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != MaxEdge || cfg.Height != 1176 {
			t.Errorf("%s fitted to %dx%d, want %dx1176", format, cfg.Width, cfg.Height, MaxEdge)
		}
		// Not smaller than the original, necessarily: a resampled PNG
		// compresses worse than a flat one. Within what a model is sent.
		if !strings.HasPrefix(mt, "image/") || len(out) > MaxBytes {
			t.Errorf("%s: %s, %d bytes from %d", format, mt, len(out), len(raw))
		}
	}
	// A tall one: the long edge is the height.
	out, _, _ := Fit(bytes.NewReader(encode(t, picture(1000, 3000), "jpeg")), "image/jpeg")
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(out))
	if cfg.Height != MaxEdge || cfg.Width != 522 {
		t.Errorf("tall image fitted to %dx%d", cfg.Width, cfg.Height)
	}
}

func TestNotAnImage(t *testing.T) {
	if _, _, err := Fit(strings.NewReader("[ 0.1] Booting Linux\n"), "text/plain"); err != ErrNotAnImage {
		t.Errorf("a log as an image: %v", err)
	}
}
