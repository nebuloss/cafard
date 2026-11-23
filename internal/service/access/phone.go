package access

import (
	"context"
	"errors"
	"time"

	"cafard/internal/store"
)

// SigninLinkLife is how long a "sign in another phone" QR code works: long
// enough to pick up a phone and scan it, short enough that a code left on a
// screen is soon worthless (docs/design.md, "Signing in").
const SigninLinkLife = 2 * time.Minute

// IssueSigninLink makes a code that signs another browser in as who: the QR
// code a phone already signed in shows. People only, for themselves. It asks
// nothing more than the sign-in itself: there is no password to ask for,
// and the code lasts two minutes.
func (s *Service) IssueSigninLink(ctx context.Context, who Who) (string, store.Link, error) {
	if err := active(who.Identity); err != nil {
		return "", store.Link{}, err
	}
	if who.Kind != store.Person {
		return "", store.Link{}, refuse(NotAllowed, "Only people sign in; %s has a token.", who.Name)
	}
	code, err := randomToken()
	if err != nil {
		return "", store.Link{}, err
	}
	l, err := s.st.CreateLink(ctx, store.Link{Kind: store.SigninLink, IdentityID: who.ID, Identity: who.Name, CreatedBy: who.Name,
		ExpiresAt: time.Now().Add(SigninLinkLife)}, hashToken(code), who.audit("showed a code to sign in another phone", who.Name))
	if err != nil {
		return "", store.Link{}, err
	}
	return code, l, nil
}

// RedeemSigninLink signs the browser that opened a sign-in link in to the
// chat app, as the person who made it, and returns its cookie.
func (s *Service) RedeemSigninLink(ctx context.Context, code, from, userAgent string) (store.Identity, string, error) {
	l, id, err := s.LinkFor(ctx, code)
	if err != nil {
		return store.Identity{}, "", err
	}
	if l.Kind != store.SigninLink {
		return store.Identity{}, "", refuse(NotAllowed, "This is not a sign-in code.")
	}
	return s.redeemSignin(ctx, l, id, from, userAgent, "signed in a phone by QR code:")
}

// redeemSignin uses a link that signs a person in, and records the
// browser's sign-in to the chat app.
func (s *Service) redeemSignin(ctx context.Context, l store.Link, id store.Identity, from, userAgent, action string) (store.Identity, string, error) {
	cookie, err := randomToken()
	if err != nil {
		return store.Identity{}, "", err
	}
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	_, err = s.st.RedeemSigninLink(ctx, l, store.Signin{Surface: ChatSurface, UserAgent: userAgent, From: from,
		ExpiresAt: time.Now().Add(signinLife[ChatSurface])}, hashToken(cookie),
		store.AuditEntry{Actor: id.Name, Action: action, Target: describeAgent(userAgent), Origin: "join · " + from})
	if errors.Is(err, store.ErrLinkSpent) {
		return store.Identity{}, "", refuse(Spent, "This link was used a moment ago. Ask for a new one.")
	}
	if err != nil {
		return store.Identity{}, "", err
	}
	return id, cookie, nil
}

// Signins lists who's own sign-ins, for their profile.
func (s *Service) Signins(ctx context.Context, who Who) ([]store.Signin, error) {
	if who.Kind != store.Person {
		return nil, refuse(NotAllowed, "Only people sign in; %s has a token.", who.Name)
	}
	return s.st.Signins(ctx, who.ID)
}

// CanDeleteFile is whether who may delete a file: its sender.
func (s *Service) CanDeleteFile(ctx context.Context, who Who, authorID int64) error {
	if err := active(who.Identity); err != nil {
		return err
	}
	if who.ID == authorID {
		return nil
	}
	return refuse(NotAllowed, "Only the member who sent a file can delete it.")
}
