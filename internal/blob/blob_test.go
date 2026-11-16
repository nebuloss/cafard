package blob

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

func newStore(t *testing.T) (*Store, *clock.Fake, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewFake(time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC))
	var cfg Config
	cfg.SetDefaults()
	b, err := New(st, dir, cfg, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return b, clk, dir
}

// gradient is an image whose top-left pixel is red and the rest blue, so
// a rotation is visible.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{0, 0, 255, 255}
			if x < w/4 && y < h/4 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// withOrientation inserts an EXIF APP1 segment with an orientation tag.
func withOrientation(t *testing.T, jpg []byte, o uint16) []byte {
	t.Helper()
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08")
	ifd := make([]byte, 2+12+4)
	binary.BigEndian.PutUint16(ifd[0:], 1)
	binary.BigEndian.PutUint16(ifd[2:], 0x0112)
	binary.BigEndian.PutUint16(ifd[4:], 3) // SHORT
	binary.BigEndian.PutUint32(ifd[6:], 1)
	binary.BigEndian.PutUint16(ifd[10:], o)
	payload := append(append([]byte("Exif\x00\x00"), tiff...), ifd...)
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

func TestImageVariantsWithOrientation(t *testing.T) {
	b, _, dir := newStore(t)
	ctx := context.Background()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, gradient(4000, 2000), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := withOrientation(t, buf.Bytes(), 6)
	if exifOrientation(raw) != 6 {
		t.Fatal("test EXIF not read back")
	}
	row, err := b.Put(ctx, bytes.NewReader(raw), "image/whatever")
	if err != nil {
		t.Fatal(err)
	}
	if row.Mime != MimeJPEG || row.Width != 2000 || row.Height != 4000 || row.AgentSHA256 == "" || row.ThumbSHA256 == "" {
		t.Fatalf("row = %+v", row)
	}
	f, agentRow, err := b.Open(ctx, row.SHA256, VariantAgent)
	if err != nil {
		t.Fatal(err)
	}
	agentBytes, _ := io.ReadAll(f)
	f.Close()
	img, err := jpeg.Decode(bytes.NewReader(agentBytes))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 784 || img.Bounds().Dy() != 1568 || agentRow.Width != 784 {
		t.Errorf("agent variant = %v (%+v)", img.Bounds(), agentRow)
	}
	// Rotated 90 degrees clockwise: the red corner is now top-right.
	r, _, bl, _ := img.At(img.Bounds().Dx()-10, 10).RGBA()
	if r < 0xa000 || bl > 0x6000 {
		t.Errorf("top-right after rotation is not red: r=%x b=%x", r, bl)
	}
	if exifOrientation(agentBytes) != 1 || bytes.Contains(agentBytes, []byte("Exif\x00\x00")) {
		t.Error("the agent variant kept EXIF")
	}
	_, thumb, err := b.Open(ctx, row.SHA256, VariantThumb)
	if err != nil || thumb.Height != ThumbLongEdge || thumb.Width != 160 {
		t.Errorf("thumb = %+v %v", thumb, err)
	}
	// Files live at blobs/<aa>/<sha>, mode 0600.
	fi, err := os.Stat(filepath.Join(dir, "blobs", row.SHA256[:2], row.SHA256))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file = %v %v", fi, err)
	}
	// The same bytes again are deduplicated.
	again, err := b.Put(ctx, bytes.NewReader(raw), MimeJPEG)
	if err != nil || again.SHA256 != row.SHA256 {
		t.Errorf("dedupe = %+v %v", again, err)
	}
}

func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	chunk := append([]byte("IHDR"), ihdr...)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, chunk...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}

func TestDecompressionBombsAndOddFilesAreRefused(t *testing.T) {
	b, _, _ := newStore(t)
	ctx := context.Background()
	_, err := b.Put(ctx, bytes.NewReader(pngHeader(8000, 8000)), MimePNG)
	var apiErr *dto.APIError
	if !asErr(err, &apiErr) || apiErr.Code != dto.CodeTooLarge {
		t.Errorf("64 MP: %v", err)
	}
	if _, err := b.Put(ctx, bytes.NewReader([]byte("just text")), MimePNG); !asErr(err, &apiErr) || apiErr.Code != dto.CodeBadRequest {
		t.Errorf("text as an image: %v", err)
	}
	big := bytes.Repeat([]byte{0}, 21<<20)
	if _, err := b.Put(ctx, bytes.NewReader(big), "text/plain"); !asErr(err, &apiErr) || apiErr.Code != dto.CodeTooLarge {
		t.Errorf("21 MiB: %v", err)
	}
	heic := append([]byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), bytes.Repeat([]byte{1}, 100)...)
	row, err := b.Put(ctx, bytes.NewReader(heic), "image/heic")
	if err != nil || row.Mime != MimeHEIC || row.AgentSHA256 != "" {
		t.Errorf("heic = %+v %v", row, err)
	}
	if _, _, err := b.Open(ctx, row.SHA256, VariantAgent); err != ErrNotFound {
		t.Errorf("heic agent variant: %v", err)
	}
	if _, _, err := b.Open(ctx, row.SHA256, ""); err != nil {
		t.Errorf("heic original: %v", err)
	}
	if _, _, err := b.Open(ctx, "../../etc/passwd", ""); err != ErrNotFound {
		t.Errorf("path traversal: %v", err)
	}
}

func asErr(err error, target **dto.APIError) bool {
	e, ok := err.(*dto.APIError)
	if ok {
		*target = e
	}
	return ok
}

func TestJanitorDeletesUnreferencedBlobsAndTrimsBySize(t *testing.T) {
	b, clk, _ := newStore(t)
	ctx := context.Background()
	var buf bytes.Buffer
	if err := png.Encode(&buf, gradient(64, 64)); err != nil {
		t.Fatal(err)
	}
	img, err := b.Put(ctx, bytes.NewReader(buf.Bytes()), MimePNG)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := b.PutText(ctx, "a snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.st.WriteBatch(ctx, []store.Entry{{Stream: "chan:general", Kind: store.KindMessage, Text: "x", Severity: 6, Sender: "a", TS: clk.Now(),
		Attachments: []store.Attachment{{Type: dto.AttachmentLog, SnapshotSHA256: kept}}}}); err != nil {
		t.Fatal(err)
	}
	// Within the grace period nothing goes.
	if err := b.DeleteUnreferenced(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(ctx, img.AgentSHA256); err != nil {
		t.Fatalf("young variant deleted: %v", err)
	}
	clk.Advance(2 * time.Hour)
	if err := b.DeleteUnreferenced(ctx); err != nil {
		t.Fatal(err)
	}
	for _, sha := range []string{img.SHA256, img.AgentSHA256, img.ThumbSHA256} {
		if _, err := b.Stat(ctx, sha); err != ErrNotFound {
			t.Errorf("%s survived: %v", sha, err)
		}
	}
	if _, err := b.Stat(ctx, kept); err != nil {
		t.Errorf("referenced snapshot deleted: %v", err)
	}
	// Size limit: oldest first.
	clk.Advance(time.Minute)
	newer, _ := b.PutText(ctx, "a newer one, a bit longer")
	if err := b.TrimTo(ctx, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(ctx, kept); err != ErrNotFound {
		t.Error("the oldest blob survived the size limit")
	}
	if _, err := b.Stat(ctx, newer); err != nil {
		t.Errorf("the newest blob was trimmed: %v", err)
	}
	// Visibility needs a visible referencing message.
	b.SetVisible(func(_ context.Context, p auth.Principal, e store.Entry) bool { return p.Name == "a" })
	if ok, _ := b.VisibleTo(ctx, newer, auth.Principal{Name: "a"}); ok {
		t.Error("an unreferenced blob is visible")
	}
}
