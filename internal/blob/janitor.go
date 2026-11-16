package blob

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/nebuloss/cafard/internal/store"
)

// Grace protects a new blob from the janitor before a message references
// it: an upload in flight, or a share staged for an hour.
const Grace = time.Hour

// Tasks are the janitor's blob passes: unreferenced blobs, then the total
// size limit (0 means none).
func (b *Store) Tasks(maxBytes int64) []store.Task {
	return []store.Task{
		{Name: "unreferenced blobs", Run: b.DeleteUnreferenced},
		{Name: "blob size limit", Run: func(ctx context.Context) error { return b.TrimTo(ctx, maxBytes) }},
	}
}

// referenced is every blob a message points at, with its variants.
func (b *Store) referenced(ctx context.Context) (map[string]bool, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT attachments FROM entries WHERE kind = 'message' AND attachments IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var atts []store.Attachment
		if json.Unmarshal([]byte(raw), &atts) != nil {
			continue
		}
		for _, a := range atts {
			if a.SHA256 != "" {
				keep[a.SHA256] = true
			}
			if a.SnapshotSHA256 != "" {
				keep[a.SnapshotSHA256] = true
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	vrows, err := b.db.QueryContext(ctx, `SELECT sha256, coalesce(agent_sha256, ''), coalesce(thumb_sha256, '') FROM blobs`)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var sha, agent, thumb string
		if err := vrows.Scan(&sha, &agent, &thumb); err != nil {
			return nil, err
		}
		if keep[sha] {
			if agent != "" {
				keep[agent] = true
			}
			if thumb != "" {
				keep[thumb] = true
			}
		}
	}
	return keep, vrows.Err()
}

// DeleteUnreferenced removes blobs no message references, once older
// than Grace. Variants of a new original are protected by its own age.
func (b *Store) DeleteUnreferenced(ctx context.Context) error {
	keep, err := b.referenced(ctx)
	if err != nil {
		return err
	}
	cutoff := b.clock.Now().Add(-Grace).UnixNano()
	rows, err := b.db.QueryContext(ctx, `SELECT sha256, created_at, coalesce(agent_sha256, ''), coalesce(thumb_sha256, '') FROM blobs`)
	if err != nil {
		return err
	}
	young := map[string]bool{}
	var old []string
	for rows.Next() {
		var (
			sha, agent, thumb string
			created           int64
		)
		if err := rows.Scan(&sha, &created, &agent, &thumb); err != nil {
			rows.Close()
			return err
		}
		if created >= cutoff {
			young[sha], young[agent], young[thumb] = true, true, true
			continue
		}
		old = append(old, sha)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	n := 0
	for _, sha := range old {
		if keep[sha] || young[sha] {
			continue
		}
		if err := b.remove(ctx, sha); err != nil {
			return err
		}
		n++
	}
	if n > 0 {
		b.log.Info("deleted unreferenced blobs", "count", n)
	}
	return nil
}

// TrimTo deletes the oldest originals, with their variants, until the
// blobs total at most limit bytes.
func (b *Store) TrimTo(ctx context.Context, limit int64) error {
	if limit <= 0 {
		return nil
	}
	var total int64
	if err := b.db.QueryRowContext(ctx, `SELECT coalesce(sum(size), 0) FROM blobs`).Scan(&total); err != nil {
		return err
	}
	if total <= limit {
		return nil
	}
	rows, err := b.db.QueryContext(ctx, `SELECT sha256, size, coalesce(agent_sha256, ''), coalesce(thumb_sha256, '') FROM blobs
		WHERE sha256 NOT IN (SELECT agent_sha256 FROM blobs WHERE agent_sha256 IS NOT NULL)
		AND sha256 NOT IN (SELECT thumb_sha256 FROM blobs WHERE thumb_sha256 IS NOT NULL)
		ORDER BY created_at`)
	if err != nil {
		return err
	}
	var victims []string
	for rows.Next() && total > limit {
		var (
			sha, agent, thumb string
			size              int64
		)
		if err := rows.Scan(&sha, &size, &agent, &thumb); err != nil {
			rows.Close()
			return err
		}
		total -= size
		victims = append(victims, sha)
		for _, v := range []string{agent, thumb} {
			if v != "" {
				if row, err := b.Stat(ctx, v); err == nil {
					total -= row.Size
				}
				victims = append(victims, v)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sha := range victims {
		if err := b.remove(ctx, sha); err != nil {
			return err
		}
	}
	if len(victims) > 0 {
		b.log.Info("deleted blobs over the size limit", "count", len(victims))
	}
	return nil
}

func (b *Store) remove(ctx context.Context, sha string) error {
	if _, err := b.db.ExecContext(ctx, `DELETE FROM blobs WHERE sha256 = ?`, sha); err != nil {
		return err
	}
	if !ValidSHA(sha) {
		return nil
	}
	if err := os.Remove(b.path(sha)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
