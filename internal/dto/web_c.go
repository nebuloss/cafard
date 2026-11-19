package dto

// Request bodies of the track C JSON API (CONTRACTS.md 2.3).

// TokenCreate is the body of POST /api/tokens. The principal is created,
// with Kind (default agent), when it does not exist.
type TokenCreate struct {
	Principal string        `json:"principal"`
	Kind      PrincipalKind `json:"kind,omitempty"`
	Label     string        `json:"label"`
	Scopes    []Scope       `json:"scopes"`
}

// PairingCreate is the body of POST /api/pairings: an existing human, or a
// name for a new one. Scopes default to read, message and ingest; grants
// ("lab:dut1=read,message") are what the person may reach with them.
type PairingCreate struct {
	Principal string   `json:"principal,omitempty"`
	NewName   string   `json:"new_name,omitempty"`
	Scopes    []Scope  `json:"scopes,omitempty"`
	Grants    []string `json:"grants,omitempty"`
}

// MarkCreate is the body of POST /api/marks.
type MarkCreate struct {
	Stream string `json:"stream"`
	Label  string `json:"label"`
}

// MarkCreated answers POST /api/marks.
type MarkCreated struct {
	Seq int64 `json:"seq"`
}
