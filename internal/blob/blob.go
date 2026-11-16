// Package blob stores files by content (sha256) under the data directory,
// and derives the image variants agents and the web UI read.
package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Variants of an image.
const (
	VariantOriginal = ""
	VariantAgent    = "agent"
	VariantThumb    = "thumb"
)

// Mime types of images cafard accepts.
const (
	MimePNG  = "image/png"
	MimeJPEG = "image/jpeg"
	MimeGIF  = "image/gif"
	MimeWebP = "image/webp"
	MimeHEIC = "image/heic"
	MimeText = "text/plain; charset=utf-8"
)

// ErrNotFound is an unknown blob or a variant that does not exist (HEIC).
var ErrNotFound = errors.New("blob not found")

// VisibleFunc reports whether p may see a message entry.
type VisibleFunc func(ctx context.Context, p auth.Principal, e store.Entry) bool

// Store is the blob store.
type Store struct {
	st    *store.Store
	db    *sql.DB
	dir   string
	cfg   Config
	clock clock.Clock
	log   *slog.Logger

	pool    chan struct{} // decoding slots
	visible VisibleFunc
	grant   GrantFunc
}

// GrantFunc lets p see a blob no message references yet (a staged share).
type GrantFunc func(ctx context.Context, sha string, p auth.Principal) bool

// SetGrant installs an extra visibility rule; call it before serving.
func (b *Store) SetGrant(g GrantFunc) { b.grant = g }

var _ deps.BlobStore = (*Store)(nil)

// New opens the store in <dataDir>/blobs.
func New(st *store.Store, dataDir string, cfg Config, c clock.Clock, log *slog.Logger) (*Store, error) {
	dir := filepath.Join(dataDir, "blobs")
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("blobs: %w", err)
	}
	workers := min(max(runtime.NumCPU()/2, 1), 4)
	return &Store{st: st, db: st.DB(), dir: dir, cfg: cfg, clock: c, log: log, pool: make(chan struct{}, workers)}, nil
}

// SetVisible installs the message visibility rule VisibleTo applies.
func (b *Store) SetVisible(v VisibleFunc) { b.visible = v }

// Config is the images section the store was built with.
func (b *Store) Config() Config { return b.cfg }

var shaRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA reports whether s looks like a blob id.
func ValidSHA(s string) bool { return shaRE.MatchString(s) }

func (b *Store) path(sha string) string {
	return filepath.Join(b.dir, sha[:2], sha)
}

// IsImage reports whether mime is one of the accepted image types.
func IsImage(mime string) bool {
	switch mime {
	case MimePNG, MimeJPEG, MimeGIF, MimeWebP, MimeHEIC:
		return true
	}
	return false
}

// Put stores r. An image's type is sniffed from its bytes (the declared
// one is only a hint); png, jpeg, gif and webp get their variants, HEIC is
// stored without any. Anything else keeps its declared type.
func (b *Store) Put(ctx context.Context, r io.Reader, mime string) (dto.Blob, error) {
	limit := b.cfg.MaxFileBytes()
	tmp, err := os.CreateTemp(filepath.Join(b.dir, "tmp"), "up-*")
	if err != nil {
		return dto.Blob{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit+1))
	if err != nil {
		tmp.Close()
		return dto.Blob{}, err
	}
	if n > limit {
		tmp.Close()
		return dto.Blob{}, dto.Errorf(dto.CodeTooLarge, "A file is larger than %d MiB, the limit.", b.cfg.MaxFileMiB)
	}
	head := make([]byte, 512)
	hn, _ := tmp.ReadAt(head, 0)
	head = head[:hn]
	if err := tmp.Close(); err != nil {
		return dto.Blob{}, err
	}
	if n == 0 {
		return dto.Blob{}, dto.Errorf(dto.CodeBadRequest, "A file is empty.")
	}
	if mime == "" || strings.HasPrefix(mime, "image/") {
		mime = Sniff(head)
		if !IsImage(mime) {
			return dto.Blob{}, dto.Errorf(dto.CodeBadRequest, "A file is not a png, jpeg, webp, gif or heic image.")
		}
	}
	sha := hex.EncodeToString(h.Sum(nil))

	if existing, err := b.Stat(ctx, sha); err == nil {
		// Seen before: refresh created_at so the janitor's grace period
		// covers the message about to reference it.
		_, err := b.db.ExecContext(ctx, `UPDATE blobs SET created_at = ? WHERE sha256 = ?`, b.clock.Now().UnixNano(), sha)
		return existing, err
	} else if !errors.Is(err, ErrNotFound) {
		return dto.Blob{}, err
	}

	row := dto.Blob{SHA256: sha, Mime: mime, Size: n, CreatedAt: dto.TimeOf(b.clock.Now().UTC())}
	if mime != MimeHEIC && IsImage(mime) {
		v, err := b.derive(ctx, tmpName)
		if err != nil {
			return dto.Blob{}, err
		}
		row.Width, row.Height = v.width, v.height
		agent, err := b.putBytes(ctx, v.agent, MimeJPEG, v.agentW, v.agentH)
		if err != nil {
			return dto.Blob{}, err
		}
		thumb, err := b.putBytes(ctx, v.thumb, MimeJPEG, v.thumbW, v.thumbH)
		if err != nil {
			return dto.Blob{}, err
		}
		row.AgentSHA256, row.ThumbSHA256 = agent, thumb
	}
	if err := b.place(tmpName, sha); err != nil {
		return dto.Blob{}, err
	}
	if err := b.insert(ctx, row); err != nil {
		return dto.Blob{}, err
	}
	return row, nil
}

// putBytes stores a derived file (a variant), which has no variants itself.
func (b *Store) putBytes(ctx context.Context, data []byte, mime string, w, h int) (string, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if _, err := b.Stat(ctx, sha); err == nil {
		return sha, nil
	}
	tmp, err := os.CreateTemp(filepath.Join(b.dir, "tmp"), "var-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := b.place(tmp.Name(), sha); err != nil {
		return "", err
	}
	return sha, b.insert(ctx, dto.Blob{SHA256: sha, Mime: mime, Size: int64(len(data)), Width: w, Height: h, CreatedAt: dto.TimeOf(b.clock.Now().UTC())})
}

// PutText stores a text blob (log snapshots).
func (b *Store) PutText(ctx context.Context, text string) (string, error) {
	blob, err := b.Put(ctx, strings.NewReader(text), MimeText)
	return blob.SHA256, err
}

func (b *Store) place(tmp, sha string) error {
	if err := os.MkdirAll(filepath.Join(b.dir, sha[:2]), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path(sha))
}

func (b *Store) insert(ctx context.Context, row dto.Blob) error {
	_, err := b.db.ExecContext(ctx, `INSERT INTO blobs(sha256, mime, size, width, height, created_at, agent_sha256, thumb_sha256)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(sha256) DO UPDATE SET created_at = excluded.created_at`,
		row.SHA256, row.Mime, row.Size, nullInt(row.Width), nullInt(row.Height), row.CreatedAt.UnixNano(),
		nullStr(row.AgentSHA256), nullStr(row.ThumbSHA256))
	return err
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Stat returns a blob's row.
func (b *Store) Stat(ctx context.Context, sha string) (dto.Blob, error) {
	var (
		row          dto.Blob
		w, h         sql.NullInt64
		created      int64
		agent, thumb sql.NullString
	)
	err := b.db.QueryRowContext(ctx, `SELECT sha256, mime, size, width, height, created_at, agent_sha256, thumb_sha256 FROM blobs WHERE sha256 = ?`, sha).
		Scan(&row.SHA256, &row.Mime, &row.Size, &w, &h, &created, &agent, &thumb)
	if errors.Is(err, sql.ErrNoRows) {
		return dto.Blob{}, ErrNotFound
	}
	if err != nil {
		return dto.Blob{}, err
	}
	row.Width, row.Height = int(w.Int64), int(h.Int64)
	row.CreatedAt = dto.TimeOf(time.Unix(0, created).UTC())
	row.AgentSHA256, row.ThumbSHA256 = agent.String, thumb.String
	return row, nil
}

// Open returns the original ("") or a variant ("agent", "thumb") of sha.
func (b *Store) Open(ctx context.Context, sha string, variant string) (io.ReadSeekCloser, dto.Blob, error) {
	if !ValidSHA(sha) {
		return nil, dto.Blob{}, ErrNotFound
	}
	row, err := b.Stat(ctx, sha)
	if err != nil {
		return nil, dto.Blob{}, err
	}
	switch variant {
	case VariantOriginal:
	case VariantAgent, VariantThumb:
		v := row.AgentSHA256
		if variant == VariantThumb {
			v = row.ThumbSHA256
		}
		if v == "" {
			return nil, dto.Blob{}, ErrNotFound
		}
		if row, err = b.Stat(ctx, v); err != nil {
			return nil, dto.Blob{}, err
		}
	default:
		return nil, dto.Blob{}, ErrNotFound
	}
	f, err := os.Open(b.path(row.SHA256))
	if errors.Is(err, os.ErrNotExist) {
		return nil, dto.Blob{}, ErrNotFound
	}
	if err != nil {
		return nil, dto.Blob{}, err
	}
	return f, row, nil
}

// ReadAll returns a blob's bytes (variant as in Open).
func (b *Store) ReadAll(ctx context.Context, sha, variant string) ([]byte, dto.Blob, error) {
	f, row, err := b.Open(ctx, sha, variant)
	if err != nil {
		return nil, dto.Blob{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	return data, row, err
}

// originals are the blobs sha is, or is a variant of.
func (b *Store) originals(ctx context.Context, sha string) ([]string, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT sha256 FROM blobs WHERE sha256 = ? OR agent_sha256 = ? OR thumb_sha256 = ?`, sha, sha, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// VisibleTo reports whether p can see at least one message referencing
// sha (as an image, the variant of one, or a log snapshot).
func (b *Store) VisibleTo(ctx context.Context, sha string, p auth.Principal) (bool, error) {
	if !ValidSHA(sha) || b.visible == nil {
		return false, nil
	}
	if b.grant != nil && b.grant(ctx, sha, p) {
		return true, nil
	}
	origs, err := b.originals(ctx, sha)
	if err != nil {
		return false, err
	}
	for _, o := range origs {
		seqs, err := b.referencing(ctx, o)
		if err != nil {
			return false, err
		}
		for _, seq := range seqs {
			e, err := b.st.Get(ctx, seq)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return false, err
			}
			if b.visible(ctx, p, e) && references(e, o) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (b *Store) referencing(ctx context.Context, sha string) ([]int64, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT seq FROM entries WHERE kind = 'message' AND attachments IS NOT NULL
		AND instr(attachments, ?) > 0 ORDER BY seq DESC LIMIT 200`, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func references(e store.Entry, sha string) bool {
	for _, a := range e.Attachments {
		if a.SHA256 == sha || a.SnapshotSHA256 == sha {
			return true
		}
	}
	return false
}

// Sniff guesses an image type from the first bytes.
func Sniff(head []byte) string {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		switch string(head[8:12]) {
		case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
			return MimeHEIC
		}
	}
	if len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP" {
		return MimeWebP
	}
	ct := http.DetectContentType(head)
	switch ct {
	case MimePNG, MimeJPEG, MimeGIF, MimeWebP:
		return ct
	}
	if bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}) {
		return MimeJPEG
	}
	return ct
}
