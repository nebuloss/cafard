package webtest

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"strconv"
	"testing"
)

// Itoa formats a seq or an id for a URL.
func Itoa(n int64) string { return strconv.FormatInt(n, 10) }

// PNG is a w x h test image.
func PNG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Multipart is an Env.Do body: a multipart form with fields and image
// files under files[].
type Multipart struct {
	Fields map[string]string
	Files  [][]byte
}

func (m Multipart) encode() (ctype string, body *bytes.Buffer) {
	body = &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range m.Fields {
		_ = mw.WriteField(k, v)
	}
	for i, f := range m.Files {
		w, _ := mw.CreateFormFile("files[]", fmt.Sprintf("IMG_%d.png", i))
		_, _ = w.Write(f)
	}
	_ = mw.Close()
	return mw.FormDataContentType(), body
}
