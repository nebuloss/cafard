package messaging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Claim durations.
const (
	defaultClaimTTL = 15 * time.Minute
	maxClaimTTL     = 24 * time.Hour
	maxClaimNote    = 500
)

// ClaimRow is a claim on a stream.
type ClaimRow struct {
	Resource  string
	Principal string
	Note      string
	ExpiresAt time.Time
}

// DTO is the claim as list_streams shows it.
func (c ClaimRow) DTO() dto.Claim {
	return dto.Claim{Principal: c.Principal, Note: c.Note, ExpiresAt: dto.TimeOf(c.ExpiresAt)}
}

// checkResource refuses what is not a log stream p may claim: one it can
// read (or the claim would say it exists) with message on it.
func (s *Service) checkResource(ctx context.Context, p auth.Principal, resource string) error {
	if store.IsMessageStream(resource) {
		return dto.Errorf(dto.CodeBadRequest, "Claims are on log streams such as serial:lab/dut1, not on %s.", resource)
	}
	if err := store.ValidateStreamID(resource); err != nil {
		return dto.Errorf(dto.CodeBadRequest, "%v.", err)
	}
	a := s.access(ctx, p)
	if !a.Can(resource, auth.ScopeRead) {
		return dto.Errorf(dto.CodeNotFound, "There is no stream %s you can read.", resource)
	}
	if !a.Can(resource, auth.ScopeMessage) {
		return dto.Errorf(dto.CodeForbidden, "Claiming %s needs message on it; ask the admin for a grant.", resource)
	}
	return nil
}

// Claim takes or renews p's claim on resource. A claim held by someone
// else is a conflict naming the holder. Every claim writes a marker into
// the resource's stream.
func (s *Service) Claim(ctx context.Context, p auth.Principal, resource string, ttl time.Duration, note string) (ClaimRow, bool, error) {
	if err := needMessage(p); err != nil {
		return ClaimRow{}, false, err
	}
	if err := s.checkResource(ctx, p, resource); err != nil {
		return ClaimRow{}, false, err
	}
	if len(note) > maxClaimNote {
		return ClaimRow{}, false, dto.Errorf(dto.CodeTooLarge, "A claim note is at most %d bytes.", maxClaimNote)
	}
	if ttl <= 0 {
		ttl = defaultClaimTTL
	}
	ttl = min(ttl, maxClaimTTL)
	if _, err := s.expire(ctx, resource); err != nil {
		return ClaimRow{}, false, err
	}
	now := s.clock.Now()
	c := ClaimRow{Resource: resource, Principal: p.Name, Note: note, ExpiresAt: now.Add(ttl).UTC()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClaimRow{}, false, err
	}
	defer tx.Rollback()
	cur, err := claimTx(ctx, tx, resource)
	renewed := false
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return ClaimRow{}, false, err
	case cur.Principal != p.Name:
		return ClaimRow{}, false, dto.Errorf(dto.CodeConflict, "%s is claimed by %s until %s%s.", resource, cur.Principal,
			cur.ExpiresAt.Format("15:04:05 UTC"), notePart(cur.Note))
	default:
		renewed = true
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO claims(resource, principal, note, expires_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(resource) DO UPDATE SET note = excluded.note, expires_at = excluded.expires_at`,
		resource, p.Name, nullStr(note), c.ExpiresAt.UnixNano()); err != nil {
		return ClaimRow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ClaimRow{}, false, err
	}
	verb := "claimed"
	if renewed {
		verb = "renewed the claim on"
	}
	text := fmt.Sprintf("%s %s %s for %s%s", p.Name, verb, resource, ttl.Round(time.Second), notePart(note))
	if err := s.marker(ctx, resource, p.Name, text); err != nil {
		return ClaimRow{}, false, err
	}
	return c, renewed, nil
}

func notePart(note string) string {
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}

func claimTx(ctx context.Context, tx *sql.Tx, resource string) (ClaimRow, error) {
	var (
		c       ClaimRow
		note    sql.NullString
		expires int64
	)
	err := tx.QueryRowContext(ctx, `SELECT resource, principal, note, expires_at FROM claims WHERE resource = ?`, resource).
		Scan(&c.Resource, &c.Principal, &note, &expires)
	c.Note, c.ExpiresAt = note.String, time.Unix(0, expires).UTC()
	return c, err
}

// Release drops p's claim. Releasing someone else's claim is refused,
// except for an admin.
func (s *Service) Release(ctx context.Context, p auth.Principal, resource string) error {
	if err := needMessage(p); err != nil {
		return err
	}
	if err := s.checkResource(ctx, p, resource); err != nil {
		return err
	}
	if _, err := s.expire(ctx, resource); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, err := claimTx(ctx, tx, resource)
	if errors.Is(err, sql.ErrNoRows) {
		return dto.Errorf(dto.CodeNotFound, "Nobody holds a claim on %s.", resource)
	}
	if err != nil {
		return err
	}
	if cur.Principal != p.Name && !p.Has(auth.ScopeAdmin) {
		return dto.Errorf(dto.CodeConflict, "%s is claimed by %s, not by you.", resource, cur.Principal)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM claims WHERE resource = ?`, resource); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	text := fmt.Sprintf("%s released %s", p.Name, resource)
	if cur.Principal != p.Name {
		text = fmt.Sprintf("%s released %s's claim on %s", p.Name, cur.Principal, resource)
	}
	return s.marker(ctx, resource, p.Name, text)
}

// expire deletes expired claims (of one resource, or all when empty),
// writing a "claim expired" marker for each.
func (s *Service) expire(ctx context.Context, resource string) (int, error) {
	now := s.clock.Now().UnixNano()
	q := `DELETE FROM claims WHERE expires_at <= ? RETURNING resource, principal`
	args := []any{now}
	if resource != "" {
		q = `DELETE FROM claims WHERE expires_at <= ? AND resource = ? RETURNING resource, principal`
		args = append(args, resource)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	var gone []ClaimRow
	for rows.Next() {
		var c ClaimRow
		if err := rows.Scan(&c.Resource, &c.Principal); err != nil {
			rows.Close()
			return 0, err
		}
		gone = append(gone, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, c := range gone {
		if err := s.marker(ctx, c.Resource, "", fmt.Sprintf("claim of %s on %s expired", c.Principal, c.Resource)); err != nil {
			return 0, err
		}
	}
	return len(gone), nil
}

// ExpireClaims is the janitor's claim pass.
func (s *Service) ExpireClaims(ctx context.Context) (int, error) { return s.expire(ctx, "") }

// listClaims returns the live claims, by resource; patterns (ids or
// globs) narrow them, nil keeps all.
func (s *Service) listClaims(ctx context.Context, patterns []string) ([]ClaimRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT resource, principal, coalesce(note, ''), expires_at FROM claims WHERE expires_at > ? ORDER BY resource`,
		s.clock.Now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClaimRow
	for rows.Next() {
		var (
			c       ClaimRow
			expires int64
		)
		if err := rows.Scan(&c.Resource, &c.Principal, &c.Note, &expires); err != nil {
			return nil, err
		}
		c.ExpiresAt = time.Unix(0, expires).UTC()
		if patterns != nil && !store.StreamMatches(c.Resource, patterns) {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ActiveClaims implements mcpserver.ClaimSource for list_streams.
func (s *Service) ActiveClaims(ctx context.Context) (map[string]dto.Claim, error) {
	list, err := s.listClaims(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]dto.Claim, len(list))
	for _, c := range list {
		out[c.Resource] = c.DTO()
	}
	return out, nil
}

// Holds reports whether p holds a live claim on resource (serial_write).
func (s *Service) Holds(ctx context.Context, p auth.Principal, resource string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM claims WHERE resource = ? AND principal = ? AND expires_at > ?`,
		resource, p.Name, s.clock.Now().UnixNano()).Scan(&n)
	return n > 0, err
}
