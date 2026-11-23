// Package blobs stores the files people and agents send, by content.
//
// A file is named by the SHA-256 of its bytes and kept at files/ab/cdef…, so
// the same photo sent twice, to two conversations or two projects, is stored
// once. What a file is called, who sent it and where it belongs is the
// database's business; this package only keeps bytes.
//
// Which blobs are still referenced is the database's to decide too: Remove
// deletes what it is told to, and the caller asks only once no message holds
// the blob any more (docs/design.md, "Delete file").
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

// ErrNotFound is a sum with no blob behind it.
var ErrNotFound = errors.New("blob not found")

// Store is a directory of blobs.
type Store struct{ dir string }

// Open uses dir, creating it. Uploads that were cut off, left in tmp/ by a
// process that stopped mid-write, are removed: nothing refers to them.
func Open(dir string) (*Store, error) {
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, fmt.Errorf("open blobs: %w", err)
	}
	left, err := os.ReadDir(tmp)
	if err != nil {
		return nil, fmt.Errorf("open blobs: %w", err)
	}
	for _, e := range left {
		os.Remove(filepath.Join(tmp, e.Name()))
	}
	return &Store{dir: dir}, nil
}

// Put stores what r yields and returns its sum and size.
func (s *Store) Put(r io.Reader) (sum string, size int64, err error) {
	w, err := s.create("put-*")
	if err != nil {
		return "", 0, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Abort()
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	return w.Commit()
}

// Writer receives a blob that is still arriving.
//
// The bytes are written to tmp/ and hashed on the way, then renamed into
// place by Commit, so a blob at its final path is always whole. While it is
// arriving, OpenStaged reads what has come so far under the key it was
// created with, which is how a live log is read before it ends. A crash
// leaves only a staged file, which the next Open removes.
type Writer struct {
	s    *Store
	f    *os.File
	h    hash.Hash
	n    int64
	path string
}

// Create starts a blob staged under key, which must be unique among the
// blobs arriving at once (a file's id).
func (s *Store) Create(key string) (*Writer, error) {
	if !validKey(key) {
		return nil, fmt.Errorf("store blob: bad staging key %q", key)
	}
	path := s.stagedPath(key)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store blob: %w", err)
	}
	return &Writer{s: s, f: f, h: sha256.New(), path: path}, nil
}

func (s *Store) create(pattern string) (*Writer, error) {
	f, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), pattern)
	if err != nil {
		return nil, fmt.Errorf("store blob: %w", err)
	}
	return &Writer{s: s, f: f, h: sha256.New(), path: f.Name()}, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	w.h.Write(p[:n])
	w.n += int64(n)
	return n, err
}

// Size is how many bytes have arrived.
func (w *Writer) Size() int64 { return w.n }

// Commit makes what arrived a blob, and returns its sum and size.
func (w *Writer) Commit() (sum string, size int64, err error) {
	defer func() {
		if err != nil {
			w.Abort()
		}
	}()
	if err = w.f.Sync(); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	if err = w.f.Close(); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	sum = hex.EncodeToString(w.h.Sum(nil))
	dst := w.s.path(sum)
	if _, statErr := os.Stat(dst); statErr == nil {
		// Stored already: the same bytes, by definition of the name.
		os.Remove(w.path)
		return sum, w.n, nil
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	if err = os.Rename(w.path, dst); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	return sum, w.n, nil
}

// Abort drops what arrived.
func (w *Writer) Abort() {
	w.f.Close()
	os.Remove(w.path)
}

// OpenStaged reads a blob that is still arriving, as far as it has come.
func (s *Store) OpenStaged(key string) (*os.File, error) {
	if !validKey(key) {
		return nil, ErrNotFound
	}
	f, err := os.Open(s.stagedPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (s *Store) stagedPath(key string) string {
	return filepath.Join(s.dir, "tmp", "staged-"+key)
}

// validKey is a staging key: digits and lowercase letters, so it can never
// name a path.
func validKey(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	for _, c := range key {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// Open returns the blob for reading.
func (s *Store) Open(sum string) (*os.File, error) {
	if !validSum(sum) {
		return nil, ErrNotFound
	}
	f, err := os.Open(s.path(sum))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Has reports whether the blob is stored.
func (s *Store) Has(sum string) bool {
	if !validSum(sum) {
		return false
	}
	_, err := os.Stat(s.path(sum))
	return err == nil
}

// Remove deletes the blob. Removing one that is not there is ErrNotFound,
// because a caller that thought it was there has its own records wrong.
func (s *Store) Remove(sum string) error {
	if !validSum(sum) {
		return ErrNotFound
	}
	err := os.Remove(s.path(sum))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (s *Store) path(sum string) string {
	return filepath.Join(s.dir, sum[:2], sum[2:])
}

// validSum is 64 lowercase hex digits. Checked before any path is built from
// a sum, so a sum from a request can never name a file outside the store.
func validSum(sum string) bool {
	if len(sum) != sha256.Size*2 {
		return false
	}
	for _, c := range sum {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
