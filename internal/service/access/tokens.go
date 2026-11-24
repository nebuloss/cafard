package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"

	"cafard/internal/store"
)

// Unauthorized is a request whose credential is missing, malformed or not
// one cafard issued.
const Unauthorized Code = "unauthorized"

// Token prefixes. They say what kind of identity a token is for, and let a
// secret scanner recognise a leaked one.
const (
	agentPrefix  = "cfd_ag_"
	devicePrefix = "cfd_dv_"
)

// tokenBytes of randomness: 256 bits, so a plain SHA-256 of the token is a
// safe thing to store and to look up by.
const tokenBytes = 32

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// hashToken is what the store keeps and looks a token up by.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueToken gives an agent or a device a new token and returns it. The old
// one, if any, stops working. Admins only; people sign in instead and have
// no token. The token is returned once and never stored: what is shown here
// is the only copy.
func (s *Service) IssueToken(ctx context.Context, who Who, target store.Identity) (string, error) {
	if err := requireAdmin(who); err != nil {
		return "", err
	}
	if target.Deleted() {
		return "", refuse(Removed, "%s has been removed from cafard.", target.Name)
	}
	prefix := agentPrefix
	switch target.Kind {
	case store.Agent:
	case store.Device:
		prefix = devicePrefix
	default:
		return "", refuse(NotAllowed, "%s is a person: people sign in, and have no token.", target.Name)
	}
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := prefix + strings.ToLower(tokenEncoding.EncodeToString(raw))
	if err := s.st.SetToken(ctx, target.ID, hashToken(token), who.audit("issued a token for", target.Name)); err != nil {
		return "", notFound(err, target.Name)
	}
	return token, nil
}

// Authenticate is the identity a token belongs to, if it may act. from is
// where the request came from, recorded as the token's last use.
func (s *Service) Authenticate(ctx context.Context, token, from string) (store.Identity, error) {
	var want store.Kind
	switch {
	case strings.HasPrefix(token, agentPrefix):
		want = store.Agent
	case strings.HasPrefix(token, devicePrefix):
		want = store.Device
	default:
		return store.Identity{}, refuse(Unauthorized, "That is not a cafard token: it starts with cfd_ag_ or cfd_dv_.")
	}
	id, err := s.st.IdentityByTokenHash(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return store.Identity{}, refuse(Unauthorized, "This token is not valid: it was never issued, or it was replaced. Ask an admin for a new one.")
	}
	if err != nil {
		return store.Identity{}, err
	}
	// A prefix that disagrees with the identity is a token someone edited.
	if id.Kind != want {
		return store.Identity{}, refuse(Unauthorized, "This token is not valid.")
	}
	if err := active(id); err != nil {
		return store.Identity{}, err
	}
	if err := s.st.TouchToken(ctx, id.ID, from); err != nil {
		return store.Identity{}, err
	}
	return id, nil
}

// Active is an identity by id, if it may still act. What was granted ahead
// of time, like an agent's upload link, is checked again with it when it is
// used: the agent may have been paused or removed since.
func (s *Service) Active(ctx context.Context, id int64) (store.Identity, error) {
	i, err := s.st.Identity(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return i, refuse(NotFound, "Member %d does not exist, or has been removed.", id)
	}
	if err != nil {
		return i, err
	}
	return i, active(i)
}
