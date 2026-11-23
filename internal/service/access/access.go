// Package access decides whether a member may do something. It is the only
// package that does: every API route and every MCP tool asks it, and none
// reads memberships to decide for itself (internal/arch holds the code to
// that).
//
// Membership of a project is the only permission (docs/design.md, "Access and
// attention"). A person or an agent reads and writes every conversation in
// its projects; a device only sends to them. Nothing else is configured.
//
// The rules, each with a test in access_test.go:
//
//  1. No agent or device can change access. Creating a person, an agent, a
//     device or a project, adding or removing a member, pausing or deleting
//     anyone: only people may, and all but membership only admins.
//  2. Only people add members to a project: a person already in it, or an
//     admin. Not to an archived project.
//  3. Agents reach only members they share a project with: a direct message
//     needs a common project. Devices are in no direct messages at all: they
//     send to projects and read nothing.
//  4. Removing a member never removes history: a deleted identity keeps its
//     row and name (store.DeleteIdentity), and loses its memberships.
//
// And three that follow from what an identity is:
//
//   - A paused or deleted identity can do nothing.
//   - An archived project can be read and not written.
//   - An admin's access to a project's content is still its membership.
//     Admins manage who is where, and may put themselves in a project, which
//     the audit log records; they do not read projects they are not in.
//
// A refusal is a *Refusal: a code a client can act on and a sentence a person
// or an agent can read, naming what to do instead where there is something.
package access

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"cafard/internal/store"
)

// Code is the kind of refusal, stable for clients to switch on.
type Code string

const (
	NotAllowed      Code = "not_allowed"
	NotAdmin        Code = "not_admin"
	NotMember       Code = "not_member"
	Archived        Code = "archived"
	Paused          Code = "paused"
	Removed         Code = "removed"
	SendOnly        Code = "send_only"
	NoSharedProject Code = "no_shared_project"
	Exists          Code = "exists"
	NotFound        Code = "not_found"
	BadName         Code = "bad_name"
	BadValue        Code = "bad_value"
	Self            Code = "self"
)

// Refusal is why something was not allowed.
type Refusal struct {
	Code    Code
	Message string
}

func (r *Refusal) Error() string { return r.Message }

func refuse(code Code, format string, args ...any) error {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Is reports whether err is a refusal with that code.
func Is(err error, code Code) bool {
	var r *Refusal
	return errors.As(err, &r) && r.Code == code
}

// Who is the member asking, and where the request came from ("admin ·
// 10.0.50.15", "chat · 10.0.50.112"), for the audit log.
type Who struct {
	store.Identity
	Origin string
}

type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

// Names. Agents, devices and projects are slugs, because people type them
// after @ and agents pass them to select_project; a person's name is their
// username, which also allows dots and underscores.
var (
	slugName   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	personName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)
)

func validName(kind store.Kind, name string) error {
	re, example := slugName, "lab-bench"
	if kind == store.Person {
		re, example = personName, "camille"
	}
	if !re.MatchString(name) {
		return refuse(BadName, "%q is not a name cafard accepts: use 2 to 32 lowercase letters, digits and hyphens, like %s.", name, example)
	}
	return nil
}

func validProjectName(name string) error {
	if !slugName.MatchString(name) {
		return refuse(BadName, "%q is not a project name cafard accepts: use 2 to 32 lowercase letters, digits and hyphens, like speedtest.", name)
	}
	return nil
}

// active refuses an identity that cannot act at all.
func active(who store.Identity) error {
	switch {
	case who.Deleted():
		return refuse(Removed, "%s has been removed from cafard.", who.Name)
	case !who.Enabled:
		return refuse(Paused, "%s is paused. An admin can switch it back on.", who.Name)
	}
	return nil
}

func (s *Service) member(ctx context.Context, p store.Project, who store.Identity) error {
	ok, err := s.st.IsMember(ctx, p.ID, who.ID)
	if err != nil {
		return err
	}
	if !ok {
		return refuse(NotMember, "%s is not a member of %s. Ask a person in the project to add it.", who.Name, p.Name)
	}
	return nil
}

func requireAdmin(who Who) error {
	if err := active(who.Identity); err != nil {
		return err
	}
	if who.Kind != store.Person {
		return refuse(NotAllowed, "%s is %s %s: only people manage access.", who.Name, article(who.Kind), who.Kind)
	}
	if !who.Admin {
		return refuse(NotAdmin, "%s is not an admin.", who.Name)
	}
	return nil
}

func article(k store.Kind) string {
	if k == store.Agent {
		return "an"
	}
	return "a"
}

func (who Who) audit(action, target string) store.AuditEntry {
	return store.AuditEntry{Actor: who.Name, Action: action, Target: target, Origin: who.Origin}
}

// CanRead is whether who may read a project's conversations and files.
func (s *Service) CanRead(ctx context.Context, who Who, p store.Project) error {
	if err := active(who.Identity); err != nil {
		return err
	}
	if who.Kind == store.Device {
		return refuse(SendOnly, "%s is a device: it sends to %s and reads nothing.", who.Name, p.Name)
	}
	return s.member(ctx, p, who.Identity)
}

// CanPost is whether who may post or upload in a project.
func (s *Service) CanPost(ctx context.Context, who Who, p store.Project) error {
	if err := active(who.Identity); err != nil {
		return err
	}
	if err := s.member(ctx, p, who.Identity); err != nil {
		return err
	}
	if p.Archived() {
		return refuse(Archived, "%s is archived (read-only). You can read it; nothing can be posted.", p.Name)
	}
	return nil
}

// CanDirectMessage is whether who may write to other directly.
func (s *Service) CanDirectMessage(ctx context.Context, who Who, other store.Identity) error {
	if err := active(who.Identity); err != nil {
		return err
	}
	if who.Kind == store.Device {
		return refuse(SendOnly, "%s is a device: it sends to projects, not to members.", who.Name)
	}
	if other.Deleted() {
		return refuse(Removed, "%s has been removed from cafard.", other.Name)
	}
	if other.Kind == store.Device {
		return refuse(SendOnly, "%s is a device: it reads nothing, so it cannot be written to.", other.Name)
	}
	if other.ID == who.ID {
		return refuse(Self, "A direct message needs someone else.")
	}
	ok, err := s.st.ShareProject(ctx, who.ID, other.ID)
	if err != nil {
		return err
	}
	if !ok {
		return refuse(NoSharedProject, "%s and %s share no project. A direct message needs a project in common.", who.Name, other.Name)
	}
	return nil
}

// AddMember puts target in p. Only a person may, and only a person in p or an
// admin; never into an archived project.
func (s *Service) AddMember(ctx context.Context, who Who, p store.Project, target store.Identity) error {
	if err := s.canChangeMembers(ctx, who, p); err != nil {
		return err
	}
	if target.Deleted() {
		return refuse(Removed, "%s has been removed from cafard.", target.Name)
	}
	err := s.st.AddMembership(ctx, p.ID, target.ID, who.ID, who.audit("added "+target.Name+" to", p.Name))
	if errors.Is(err, store.ErrExists) {
		return refuse(Exists, "%s is already a member of %s.", target.Name, p.Name)
	}
	return err
}

// RemoveMember takes target out of p, on the same terms as AddMember.
func (s *Service) RemoveMember(ctx context.Context, who Who, p store.Project, target store.Identity) error {
	if err := s.canChangeMembers(ctx, who, p); err != nil {
		return err
	}
	err := s.st.RemoveMembership(ctx, p.ID, target.ID, who.audit("removed "+target.Name+" from", p.Name))
	if errors.Is(err, store.ErrNotFound) {
		return refuse(NotMember, "%s is not a member of %s.", target.Name, p.Name)
	}
	return err
}

func (s *Service) canChangeMembers(ctx context.Context, who Who, p store.Project) error {
	if err := active(who.Identity); err != nil {
		return err
	}
	if who.Kind != store.Person {
		return refuse(NotAllowed, "%s is %s %s: only people add or remove members.", who.Name, article(who.Kind), who.Kind)
	}
	if p.Archived() {
		return refuse(Archived, "%s is archived: its members cannot change until it is unarchived.", p.Name)
	}
	if who.Admin {
		return nil
	}
	return s.member(ctx, p, who.Identity)
}

// NewIdentity is what an admin fills in to create someone.
type NewIdentity struct {
	Kind        store.Kind
	Name        string
	DisplayName string
	Description string
}

// CreateIdentity creates a person, an agent or a device. Admins only.
func (s *Service) CreateIdentity(ctx context.Context, who Who, n NewIdentity) (store.Identity, error) {
	if err := requireAdmin(who); err != nil {
		return store.Identity{}, err
	}
	switch n.Kind {
	case store.Person, store.Agent, store.Device:
	default:
		return store.Identity{}, refuse(BadValue, "%q is not a kind: use person, agent or device.", n.Kind)
	}
	if err := validName(n.Kind, n.Name); err != nil {
		return store.Identity{}, err
	}
	id, err := s.st.CreateIdentity(ctx, store.Identity{
		Name: n.Name, Kind: n.Kind, DisplayName: n.DisplayName, Description: n.Description,
	}, who.audit("created "+string(n.Kind), n.Name))
	if errors.Is(err, store.ErrExists) {
		return store.Identity{}, refuse(Exists, "%s already exists. Names are shared by people, agents and devices, removed ones included.", n.Name)
	}
	return id, err
}

// SetPaused pauses or resumes target. Admins only, and not themselves: an
// admin who paused themselves could not switch themselves back.
func (s *Service) SetPaused(ctx context.Context, who Who, target store.Identity, paused bool) error {
	if err := s.manage(who, target); err != nil {
		return err
	}
	action := "paused"
	if !paused {
		action = "resumed"
	}
	return notFound(s.st.SetEnabled(ctx, target.ID, !paused, who.audit(action, target.Name)), target.Name)
}

// SetDescription changes what an agent's or device's machine is said to do.
func (s *Service) SetDescription(ctx context.Context, who Who, target store.Identity, description string) error {
	if err := requireAdmin(who); err != nil {
		return err
	}
	return notFound(s.st.SetDescription(ctx, target.ID, description, who.audit("changed the description of", target.Name)), target.Name)
}

// DeleteIdentity removes target from cafard. Admins only, and not themselves.
func (s *Service) DeleteIdentity(ctx context.Context, who Who, target store.Identity) error {
	if err := s.manage(who, target); err != nil {
		return err
	}
	return notFound(s.st.DeleteIdentity(ctx, target.ID, who.audit("deleted "+string(target.Kind), target.Name)), target.Name)
}

func (s *Service) manage(who Who, target store.Identity) error {
	if err := requireAdmin(who); err != nil {
		return err
	}
	if target.ID == who.ID {
		return refuse(Self, "You cannot do that to yourself.")
	}
	return nil
}

// CreateProject creates a project. Admins only.
func (s *Service) CreateProject(ctx context.Context, who Who, name string, retentionDays int, quotaBytes int64) (store.Project, error) {
	if err := requireAdmin(who); err != nil {
		return store.Project{}, err
	}
	if err := validProjectName(name); err != nil {
		return store.Project{}, err
	}
	if retentionDays <= 0 || quotaBytes <= 0 {
		return store.Project{}, refuse(BadValue, "Retention and quota must both be more than zero.")
	}
	p, err := s.st.CreateProject(ctx, store.Project{Name: name, RetentionDays: retentionDays, QuotaBytes: quotaBytes},
		who.audit("created project", name))
	if errors.Is(err, store.ErrExists) {
		return store.Project{}, refuse(Exists, "%s already exists.", name)
	}
	return p, err
}

// SetArchived archives p, or brings it back. Admins only.
func (s *Service) SetArchived(ctx context.Context, who Who, p store.Project, archived bool) error {
	if err := requireAdmin(who); err != nil {
		return err
	}
	action := "archived project"
	if !archived {
		action = "unarchived project"
	}
	return notFound(s.st.SetArchived(ctx, p.ID, archived, who.audit(action, p.Name)), p.Name)
}

// DeleteProject deletes p and every membership of it. Admins only.
func (s *Service) DeleteProject(ctx context.Context, who Who, p store.Project) error {
	if err := requireAdmin(who); err != nil {
		return err
	}
	return notFound(s.st.DeleteProject(ctx, p.ID, who.audit("deleted project", p.Name)), p.Name)
}

func notFound(err error, name string) error {
	if errors.Is(err, store.ErrNotFound) {
		return refuse(NotFound, "%s does not exist, or has been removed.", name)
	}
	return err
}
