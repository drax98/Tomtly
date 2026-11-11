package store

import (
	"errors"
	"time"
)

const maxAudit = 10000

// ErrExists means a user with that name already exists, or, for the first user, that setup is done.
var ErrExists = errors.New("already exists")

// ErrLastAdmin means the change would leave no enabled admin.
var ErrLastAdmin = errors.New("that would leave no enabled admin")

// User is a person who may use the API. Hash is the password hash and never leaves the service.
type User struct {
	ID       string
	Username string
	Hash     string
	Role     string
	Disabled bool
	Created  time.Time
}

// UserChange is what UpdateUser changes. Any change ends the user's sessions, except KeepSession, which survives.
type UserChange struct {
	Role        *string
	Disabled    *bool
	Hash        *string
	KeepSession string
	// DropTokens deletes the user's API tokens too, since one could be why the password is changing.
	DropTokens bool
}

// Token is an API token's record. The secret is never stored, only its hash.
type Token struct {
	ID       string
	UserID   string
	Owner    string // the owner's username, filled in by the lists
	Name     string
	Role     string
	Created  time.Time
	LastUsed time.Time // zero if never
}

// AuditEntry is one thing a person did through the API.
type AuditEntry struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
	IP     string    `json:"ip"`
}
