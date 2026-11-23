package access

import (
	"context"
	"errors"
	"time"

	"cafard/internal/store"
)

// One-time links (docs/design.md, "Creating an identity"). A link is not a
// credential: it carries a code that is exchanged, once, for one: a password
// the person chooses, or an agent's token. A screenshot of its QR code is
// worthless once it has been used.

// LinkLife bounds how long a link may wait. 24 hours by default, 7 days at
// most: a link is meant to be used now, by someone who is expecting it.
const (
	DefaultLinkLife = 24 * time.Hour
	MaxLinkLife     = 7 * 24 * time.Hour
)

// Spent is a link that was used, cancelled, or waited too long.
const Spent Code = "spent"

// IssueLink makes a one-time link for target and returns its code with the
// link. For a person, it lets them choose a password: a join link if they
// have none, a password link if they have one, which also ends every
// sign-in when used. For an agent, it is exchanged by the install script for
// the agent's token. Devices have no link: an admin gives their token to the
// script that sends. Admins only.
func (s *Service) IssueLink(ctx context.Context, who Who, target store.Identity, life time.Duration) (code string, l store.Link, err error) {
	if err := requireAdmin(who); err != nil {
		return "", store.Link{}, err
	}
	if target.Deleted() {
		return "", store.Link{}, refuse(Removed, "%s has been removed from cafard.", target.Name)
	}
	if life <= 0 {
		life = DefaultLinkLife
	}
	if life > MaxLinkLife {
		return "", store.Link{}, refuse(BadValue, "A link waits 7 days at most.")
	}
	kind := store.AgentLink
	action := "made an install link for"
	switch target.Kind {
	case store.Agent:
	case store.Person:
		kind, action = store.PersonLink, "made a join link for"
		if _, err := s.st.PasswordHash(ctx, target.ID); err == nil {
			kind, action = store.PasswordLink, "made a password link for"
		} else if !errors.Is(err, store.ErrNotFound) {
			return "", store.Link{}, err
		}
	default:
		return "", store.Link{}, refuse(NotAllowed, "%s is a device: give it a token instead (it has no link to open).", target.Name)
	}
	code, err = randomToken()
	if err != nil {
		return "", store.Link{}, err
	}
	l, err = s.st.CreateLink(ctx, store.Link{Kind: kind, IdentityID: target.ID, Identity: target.Name, CreatedBy: who.Name,
		ExpiresAt: time.Now().Add(life)}, hashToken(code), who.audit(action, target.Name))
	if err != nil {
		return "", store.Link{}, err
	}
	return code, l, nil
}

// LinkFor is the link a code opens and who it is for, if it can still be
// used. Anyone may ask: holding the code is what the link is.
func (s *Service) LinkFor(ctx context.Context, code string) (store.Link, store.Identity, error) {
	spent := refuse(Spent, "This link no longer works: it was used, cancelled, or waited too long. Ask an admin for a new one.")
	if code == "" || len(code) > 128 {
		return store.Link{}, store.Identity{}, spent
	}
	l, err := s.st.LinkByHash(ctx, hashToken(code))
	if errors.Is(err, store.ErrNotFound) {
		return store.Link{}, store.Identity{}, spent
	}
	if err != nil {
		return store.Link{}, store.Identity{}, err
	}
	if l.State(time.Now()) != "waiting" {
		return store.Link{}, store.Identity{}, spent
	}
	id, err := s.st.Identity(ctx, l.IdentityID)
	if err != nil {
		return store.Link{}, store.Identity{}, err
	}
	if err := active(id); err != nil {
		return store.Link{}, store.Identity{}, err
	}
	return l, id, nil
}

// ChoosePassword uses a person's join or password link to set their
// password. Every sign-in they had ends.
func (s *Service) ChoosePassword(ctx context.Context, code, password, from string) (store.Identity, error) {
	l, id, err := s.LinkFor(ctx, code)
	if err != nil {
		return store.Identity{}, err
	}
	if l.Kind != store.PersonLink && l.Kind != store.PasswordLink {
		return store.Identity{}, refuse(NotAllowed, "This link connects an agent: run it with curl on the agent's machine.")
	}
	if err := validPassword(password); err != nil {
		return store.Identity{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return store.Identity{}, err
	}
	action := "joined with their link"
	if l.Kind == store.PasswordLink {
		action = "set a new password with their link"
	}
	err = s.st.RedeemPasswordLink(ctx, l, hash, from, store.AuditEntry{Actor: id.Name, Action: action, Target: id.Name, Origin: "join · " + from})
	if errors.Is(err, store.ErrLinkSpent) {
		return store.Identity{}, refuse(Spent, "This link was used a moment ago. Ask an admin for a new one.")
	}
	return id, err
}

// RedeemAgentLink exchanges an agent's install link for its token, which is
// returned once. The agent's previous token stops working.
func (s *Service) RedeemAgentLink(ctx context.Context, code, from string) (store.Identity, string, error) {
	l, id, err := s.LinkFor(ctx, code)
	if err != nil {
		return store.Identity{}, "", err
	}
	if l.Kind != store.AgentLink {
		return store.Identity{}, "", refuse(NotAllowed, "This link is a person's: open it in a browser.")
	}
	raw, err := randomToken()
	if err != nil {
		return store.Identity{}, "", err
	}
	token := agentPrefix + raw
	err = s.st.RedeemAgentLink(ctx, l, hashToken(token), from,
		store.AuditEntry{Actor: id.Name, Action: "redeemed its install link", Target: id.Name, Origin: "join · " + from})
	if errors.Is(err, store.ErrLinkSpent) {
		return store.Identity{}, "", refuse(Spent, "This link was used a moment ago. Ask an admin for a new one.")
	}
	return id, token, err
}

// CancelLink stops a waiting link. Admins only.
func (s *Service) CancelLink(ctx context.Context, who Who, l store.Link) error {
	if err := requireAdmin(who); err != nil {
		return err
	}
	err := s.st.CancelLink(ctx, l.ID, who.audit("cancelled the link for", l.Identity))
	if errors.Is(err, store.ErrNotFound) {
		return refuse(Spent, "That link no longer works already.")
	}
	return err
}

// SetProjectLimits changes a project's retention and quota. Admins only.
func (s *Service) SetProjectLimits(ctx context.Context, who Who, p store.Project, retentionDays int, quotaBytes int64) error {
	if err := requireAdmin(who); err != nil {
		return err
	}
	if retentionDays <= 0 || quotaBytes <= 0 {
		return refuse(BadValue, "Retention and quota must both be more than zero.")
	}
	return notFound(s.st.SetProjectLimits(ctx, p.ID, retentionDays, quotaBytes,
		who.audit("changed retention and quota of", p.Name)), p.Name)
}
