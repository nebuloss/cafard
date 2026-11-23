package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestPutStoresByContent(t *testing.T) {
	s, dir := open(t)
	body := "[  212.481130] wl0: dhd_bus_rxctl: resumed on timeout\n"
	sum, size, err := s.Put(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(body))
	if sum != hex.EncodeToString(want[:]) || size != int64(len(body)) {
		t.Fatalf("sum %s size %d", sum, size)
	}
	if _, err := os.Stat(filepath.Join(dir, sum[:2], sum[2:])); err != nil {
		t.Errorf("not at files/ab/cdef…: %v", err)
	}

	f, err := s.Open(sum)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != body {
		t.Errorf("read back %q", got)
	}
}

func TestSameBytesAreStoredOnce(t *testing.T) {
	s, dir := open(t)
	a, _, _ := s.Put(strings.NewReader("photo"))
	b, _, _ := s.Put(strings.NewReader("photo"))
	if a != b {
		t.Fatalf("two sums for the same bytes: %s %s", a, b)
	}
	tmp, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(tmp) != 0 {
		t.Errorf("the second Put left %d temporary files", len(tmp))
	}
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestCutUploadLeavesNothing(t *testing.T) {
	s, dir := open(t)
	if _, _, err := s.Put(io.MultiReader(strings.NewReader("half"), failing{})); err == nil {
		t.Fatal("a failed read was stored")
	}
	tmp, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(tmp) != 0 {
		t.Errorf("a cut upload left %d temporary files", len(tmp))
	}
}

func TestOpenClearsLeftoverUploads(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o700)
	os.WriteFile(filepath.Join(dir, "tmp", "put-123"), []byte("half"), 0o600)
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	tmp, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(tmp) != 0 {
		t.Errorf("Open kept %d leftover uploads", len(tmp))
	}
}

// A live upload is readable while it arrives, under its key, and becomes an
// ordinary blob when it ends.
func TestStagedBlobIsReadableBeforeItEnds(t *testing.T) {
	s, _ := open(t)
	w, err := s.Create("17")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("17"); err == nil {
		t.Error("two blobs staged under one key")
	}
	w.Write([]byte("[ 1.0] boot\n"))
	r, err := s.OpenStaged("17")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != "[ 1.0] boot\n" {
		t.Errorf("read while arriving: %q", got)
	}
	w.Write([]byte("[ 2.0] wl0: link down\n"))
	if w.Size() != 34 {
		t.Errorf("size %d", w.Size())
	}
	sum, size, err := w.Commit()
	if err != nil || size != 34 || !s.Has(sum) {
		t.Fatalf("commit: %s %d %v", sum, size, err)
	}
	if _, err := s.OpenStaged("17"); !errors.Is(err, ErrNotFound) {
		t.Errorf("still staged after commit: %v", err)
	}
	for _, bad := range []string{"", "../x", "A", "a/b"} {
		if _, err := s.Create(bad); err == nil {
			t.Errorf("Create(%q) accepted", bad)
		}
		if _, err := s.OpenStaged(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("OpenStaged(%q): %v", bad, err)
		}
	}
}

func TestRemove(t *testing.T) {
	s, _ := open(t)
	sum, _, _ := s.Put(strings.NewReader("secret in a log"))
	if err := s.Remove(sum); err != nil {
		t.Fatal(err)
	}
	if s.Has(sum) {
		t.Error("still there after Remove")
	}
	if err := s.Remove(sum); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove: %v, want ErrNotFound", err)
	}
}

// A sum is checked before it becomes a path, so one taken from a request
// cannot reach outside the store.
func TestSumsThatAreNotSumsFindNothing(t *testing.T) {
	s, _ := open(t)
	for _, sum := range []string{"", "../../etc/passwd", strings.Repeat("A", 64), strings.Repeat("0", 63), strings.Repeat("0", 62) + "/x"} {
		if _, err := s.Open(sum); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q): %v", sum, err)
		}
		if s.Has(sum) {
			t.Errorf("Has(%q)", sum)
		}
		if err := s.Remove(sum); !errors.Is(err, ErrNotFound) {
			t.Errorf("Remove(%q): %v", sum, err)
		}
	}
}
