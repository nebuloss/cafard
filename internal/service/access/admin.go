package access

import (
	"strings"

	"cafard/internal/store"
)

// The admin is the box's operator, as in claudication: one password opens
// the admin UI, and `cafard admin` on the box acts as the same admin. The
// admin is not a member. It is in no project, sends nothing, reads nothing,
// and no person's password opens the admin UI, so a person's stolen
// password reaches the chat and never the place where identities are made.
//
// The password's hash is in the configuration file, admin-password-hash,
// written there by `cafard passwd`: like the listeners, it is the box's to
// decide, and a lost password is replaced by whoever can edit that file.

// adminName is how the admin appears in the audit log and in what it made.
const adminName = "admin"

// Admin is who the admin UI and `cafard admin` act as; origin says where
// from ("admin · 10.0.50.15", "cli").
func Admin(origin string) Who {
	return Who{Identity: store.Identity{Name: adminName, Kind: store.Person, Admin: true, Enabled: true}, Origin: origin}
}

// MinAdminPassword is claudication's minimum for its admin password: this is
// one operator's password on an internal listener, rate-limited, not a
// person's on the internet.
const MinAdminPassword = 8

// HashAdminPassword is the admin-password-hash for password.
func HashAdminPassword(password string) (string, error) {
	switch {
	case len(password) < MinAdminPassword:
		return "", refuse(BadValue, "Use at least %d characters.", MinAdminPassword)
	case len(password) > MaxPassword:
		return "", refuse(BadValue, "Use at most %d characters.", MaxPassword)
	}
	return hashPassword(password)
}

// CheckAdminPassword is whether password matches hash. With no hash set it
// is false, after the same work, so the answer takes as long either way.
func CheckAdminPassword(hash, password string) bool {
	if !strings.HasPrefix(hash, "$argon2id$") {
		checkPassword(dummyHash, password)
		return false
	}
	return checkPassword(hash, password)
}
