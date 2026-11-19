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

// Tasks are the janitor's blob passes: unreferenced blobs, then the size
// limits. maxBytes (0: none) is for the blobs outside projects; projects
// gives each project's own limit, over the blobs its messages reference.
func (b *Store) Tasks(maxBytes int64, projects store.ProjectLimits) []store.Task {
	return []store.Task{
		{Name: "unreferenced blobs", Run: b.DeleteUnreferenced},
		{Name: "blob size limit", Run: func(ctx context.Context) error { return b.TrimTo(ctx, maxBytes) }},
		{Name: "blob size limit per project", Run: func(ctx context.Context) error { return b.TrimProjects(ctx, projects) }},
	}
}

// referenced is every blob a message points at, with its variants.
func (b *Store) referenced(ctx context.Context) (map[string]bool, error) {
	by, err := b.referencedBy(ctx)
	if err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(by))
	for sha := range by {
		keep[sha] = true
	}
	return keep, nil
}

// referencedBy maps every blob a message points at, variants included, to
// the projects of those messages ("" for a direct message).
func (b *Store) referencedBy(ctx context.Context) (map[string]map[string]bool, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT stream, attachments FROM entries WHERE kind = 'message' AND attachments IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	by := map[string]map[string]bool{}
	add := func(sha, project string) {
		if by[sha] == nil {
			by[sha] = map[string]bool{}
		}
		by[sha][project] = true
	}
	for rows.Next() {
		var stream, raw string
		if err := rows.Scan(&stream, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var atts []store.Attachment
		if json.Unmarshal([]byte(raw), &atts) != nil {
			continue
		}
		project := store.ProjectOf(stream)
		for _, a := range atts {
			if a.SHA256 != "" {
				add(a.SHA256, project)
			}
			if a.SnapshotSHA256 != "" {
				add(a.SnapshotSHA256, project)
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
		for project := range by[sha] {
			for _, v := range []string{agent, thumb} {
				if v != "" {
					add(v, project)
				}
			}
		}
	}
	return by, vrows.Err()
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

// TrimTo deletes the oldest originals outside projects (those of direct
// messages, and those no message references yet), with their variants,
// until they total at most limit bytes.
func (b *Store) TrimTo(ctx context.Context, limit int64) error {
	if limit <= 0 {
		return nil
	}
	by, err := b.referencedBy(ctx)
	if err != nil {
		return err
	}
	return b.trim(ctx, limit, func(sha string) bool {
		for project := range by[sha] {
			if project != "" {
				return false
			}
		}
		return true
	})
}

// TrimProjects applies each project's blob limit to the originals its
// messages reference, oldest first. A blob another project shares goes too.
func (b *Store) TrimProjects(ctx context.Context, projects store.ProjectLimits) error {
	if projects == nil {
		return nil
	}
	limits, err := projects(ctx)
	if err != nil {
		return err
	}
	var by map[string]map[string]bool
	for name, lim := range limits {
		if lim.BlobsMaxBytes <= 0 {
			continue
		}
		if by == nil {
			if by, err = b.referencedBy(ctx); err != nil {
				return err
			}
		}
		if err := b.trim(ctx, lim.BlobsMaxBytes, func(sha string) bool { return by[sha][name] }); err != nil {
			return err
		}
	}
	return nil
}

// trim deletes the oldest originals in, with their variants, until those
// total at most limit bytes.
func (b *Store) trim(ctx context.Context, limit int64, in func(sha string) bool) error {
	type original struct {
		sha, agent, thumb string
		size              int64
	}
	rows, err := b.db.QueryContext(ctx, `SELECT o.sha256, o.size + coalesce(a.size, 0) + coalesce(t.size, 0),
		coalesce(o.agent_sha256, ''), coalesce(o.thumb_sha256, '') FROM blobs o
		LEFT JOIN blobs a ON a.sha256 = o.agent_sha256 LEFT JOIN blobs t ON t.sha256 = o.thumb_sha256
		WHERE o.sha256 NOT IN (SELECT agent_sha256 FROM blobs WHERE agent_sha256 IS NOT NULL)
		AND o.sha256 NOT IN (SELECT thumb_sha256 FROM blobs WHERE thumb_sha256 IS NOT NULL)
		ORDER BY o.created_at`)
	if err != nil {
		return err
	}
	var (
		originals []original
		total     int64
	)
	for rows.Next() {
		var o original
		if err := rows.Scan(&o.sha, &o.size, &o.agent, &o.thumb); err != nil {
			rows.Close()
			return err
		}
		if in(o.sha) {
			originals = append(originals, o)
			total += o.size
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var victims []string
	for _, o := range originals {
		if total <= limit {
			break
		}
		total -= o.size
		victims = append(victims, o.sha)
		for _, v := range []string{o.agent, o.thumb} {
			if v != "" {
				victims = append(victims, v)
			}
		}
	}
	for _, sha := range victims {
		if err := b.remove(ctx, sha); err != nil {
			return err
		}
	}
	if len(victims) > 0 {
		b.log.Info("deleted blobs over a size limit", "count", len(victims))
	}
	return nil
}

func (b *Store) remove(ctx context.Context, sha string) error {
	if _, err := b.db.ExecContext(ctx, `DELETE FROM blobs WHERE sha256 = ?`, sha); err != nil {
		return err
	}
	if !validSHA(sha) {
		return nil
	}
	if err := os.Remove(b.path(sha)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
