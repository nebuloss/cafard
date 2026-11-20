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
//
// The bytes are written to tmp/ and hashed on the way, then renamed into
// place, so a blob at its final path is always whole: a reader never sees a
// file that is still arriving, and a crash leaves only a temporary file that
// the next Open removes.
func (s *Store) Put(r io.Reader) (sum string, size int64, err error) {
	f, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "put-*")
	if err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()

	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	if err = f.Sync(); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	if err = f.Close(); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}

	sum = hex.EncodeToString(h.Sum(nil))
	dst := s.path(sum)
	if _, statErr := os.Stat(dst); statErr == nil {
		// Stored already: the same bytes, by definition of the name.
		os.Remove(tmp)
		return sum, size, nil
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	if err = os.Rename(tmp, dst); err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	return sum, size, nil
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
