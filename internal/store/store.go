// Package store keeps the service's own state. Config, secrets files and scripts stay files, so people can edit and
// version them; everything the service itself writes lives here.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound means there's no such item.
var ErrNotFound = errors.New("not found")

// Event is one entry in the automation activity log.
type Event struct {
	Time    time.Time `json:"time"`
	Script  string    `json:"script"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}

// AddedExtension is an extension added on the page, with its settings, which can hold secrets.
type AddedExtension struct {
	Source   string         `json:"source"`
	Settings map[string]any `json:"settings"`
}

// Violation is something an extension tried and wasn't allowed.
type Violation struct {
	Time      time.Time `json:"time"`
	Extension string    `json:"extension"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
}

// Activity is where an extension's allowed traffic went: one row for each kind and destination.
type Activity struct {
	Extension   string    `json:"extension"`
	Kind        string    `json:"kind"`
	Destination string    `json:"destination"`
	Count       int64     `json:"count"`
	First       time.Time `json:"first"`
	Last        time.Time `json:"last"`
}

// MarketState is what this home last saw from the marketplace. Zero if it has seen nothing.
type MarketState struct {
	Newest, NewestSigned time.Time
	Revoked              json.RawMessage
}

// Store is every read and write of the service's state. All of it must be safe for concurrent use.
// RemovedDevice is a device the owner took out of the system. Its name is kept so it can be offered back while the
// extension isn't reporting it.
type RemovedDevice struct {
	ID        string `json:"id"`
	Extension string `json:"extension"`
	Name      string `json:"name"`
	Product   string `json:"product,omitempty"`
}

type Store interface {
	// ScriptsLive returns which scripts are live. Scripts it doesn't mention are in shadow mode.
	ScriptsLive(ctx context.Context) (map[string]bool, error)
	SetScriptLive(ctx context.Context, name string, live bool) error

	// ScriptGrants returns what the owner approved for each script, as access keys.
	ScriptGrants(ctx context.Context) (map[string][]string, error)
	// SetScriptGrants approves or withdraws the keys for one script, all or none. Other keys are left as they are.
	SetScriptGrants(ctx context.Context, script string, keys []string, granted bool) error

	AppendEvents(ctx context.Context, events []Event) error
	// RecentEvents returns up to limit events, oldest first.
	RecentEvents(ctx context.Context, limit int) ([]Event, error)

	AddedExtensions(ctx context.Context) ([]AddedExtension, error)
	// SaveAddedExtension adds or replaces by source.
	SaveAddedExtension(ctx context.Context, extension AddedExtension) error
	DeleteAddedExtension(ctx context.Context, source string) error

	// Grants returns the access an extension's owner approved, as access keys. Empty if none.
	Grants(ctx context.Context, extension string) ([]string, error)
	// SetGrants replaces them. No grants at all removes the record.
	SetGrants(ctx context.Context, extension string, grants []string) error

	// WebhookTokenHash returns the SHA-256 (hex) of an extension's webhook token, or ErrNotFound. The token itself is
	// never stored, so it can only be shown when it is made.
	WebhookTokenHash(ctx context.Context, extension string) (string, error)
	// SetWebhookTokenHash replaces it.
	SetWebhookTokenHash(ctx context.Context, extension, hash string) error
	DeleteWebhookToken(ctx context.Context, extension string) error

	RecordViolation(ctx context.Context, violation Violation) error
	// Violations returns the newest first, up to limit.
	Violations(ctx context.Context, extension string, limit int) ([]Violation, error)
	ClearViolations(ctx context.Context, extension string) error

	// RecordActivity adds to the row for this kind and destination, keeping its first and latest time.
	RecordActivity(ctx context.Context, activity Activity) error
	// Activity returns the most recently used first, up to limit.
	Activity(ctx context.Context, extension string, limit int) ([]Activity, error)
	DeleteActivity(ctx context.Context, extension string) error

	// RemovedDevices lists the devices the owner took out of the system, oldest id first.
	RemovedDevices(ctx context.Context) ([]RemovedDevice, error)
	// AddRemovedDevice records one, replacing any earlier record of it.
	AddRemovedDevice(ctx context.Context, device RemovedDevice) error
	// DeleteRemovedDevice brings one back. Nothing happens if it isn't recorded.
	DeleteRemovedDevice(ctx context.Context, id string) error

	// Rooms is every room in order, and whether config.json's rooms were copied in yet.
	Rooms(ctx context.Context) (names []string, seeded bool, err error)
	// SetRooms replaces the list and its order, and marks the copy from config.json done.
	SetRooms(ctx context.Context, names []string) error
	// RenameRoom renames a room and moves its devices with it. ErrNoRoom if there is none by that name.
	RenameRoom(ctx context.Context, from, to string) error
	// DeleteRoom removes a room. Its devices stay, in no room. ErrNoRoom if there is none by that name.
	DeleteRoom(ctx context.Context, name string) error

	// DeviceOverrides is every device the owner renamed or moved to another room.
	DeviceOverrides(ctx context.Context) ([]DeviceOverride, error)
	// SetDeviceOverride replaces one. With no name and no room it is deleted, and the device is as its integration and
	// config.json say.
	SetDeviceOverride(ctx context.Context, o DeviceOverride) error

	// HomeSettings is the home's language, timezone and location; never set, it is all empty.
	HomeSettings(ctx context.Context) (HomeSettings, error)
	SetHomeSettings(ctx context.Context, h HomeSettings) error

	// Dashboard is the widgets on Home, in order. Nil if it was never arranged.
	Dashboard(ctx context.Context) ([]string, error)
	SetDashboard(ctx context.Context, widgets []string) error

	// DevicePins is every device's pinned values, by device id, in the order they show on its card.
	DevicePins(ctx context.Context) (map[string][]string, error)
	// SetDevicePins replaces one device's pins. None clears them.
	SetDevicePins(ctx context.Context, device string, dps []string) error

	// ExtensionStorage returns everything an extension saved through ctx.storage.
	ExtensionStorage(ctx context.Context, extension string) (map[string]json.RawMessage, error)
	SetExtensionValue(ctx context.Context, extension, key string, value json.RawMessage) error
	DeleteExtensionValue(ctx context.Context, extension, key string) error
	DeleteExtensionStorage(ctx context.Context, extension string) error

	// SavePackage keeps an extension's code archive, downloaded or uploaded, as gzipped tar.
	SavePackage(ctx context.Context, key string, archive []byte) error
	// Package returns one, or ErrNotFound.
	Package(ctx context.Context, key string) ([]byte, error)
	DeletePackage(ctx context.Context, key string) error

	// InstallKey returns this install's key, keeping candidate if there is none yet. Whichever is kept comes back.
	InstallKey(ctx context.Context, candidate []byte) ([]byte, error)

	MarketState(ctx context.Context) (MarketState, error)
	SaveMarketState(ctx context.Context, state MarketState) error

	// CreateFirstUser adds a user only while there are none, atomically; otherwise ErrExists.
	CreateFirstUser(ctx context.Context, user User) error
	// CreateUser adds a user; the name is unique ignoring case, else ErrExists.
	CreateUser(ctx context.Context, user User) error
	// UserByName ignores case. ErrNotFound if there is no such user.
	UserByName(ctx context.Context, username string) (User, error)
	UserByID(ctx context.Context, id string) (User, error)
	// ListUsers returns the oldest first.
	ListUsers(ctx context.Context) ([]User, error)
	// UpdateUser applies the change and ends the user's sessions, atomically with the check that an enabled admin
	// remains (ErrLastAdmin), and with deleting their API tokens if the change says to. ErrNotFound if there is no such user.
	UpdateUser(ctx context.Context, id string, change UserChange) (User, error)
	// DeleteUser removes the user with their sessions and tokens. ErrLastAdmin if they are the last enabled admin.
	DeleteUser(ctx context.Context, id string) error

	// CreateSession stores a session by the hash of its secret.
	CreateSession(ctx context.Context, hash, userID string, now time.Time) error
	// SessionUser returns the user of a live session and counts the request as activity. A session is dead after
	// absolute from creation or idle without use; the answer is then ErrNotFound.
	SessionUser(ctx context.Context, hash string, now time.Time, idle, absolute time.Duration) (User, error)
	DeleteSession(ctx context.Context, hash string) error
	// DeleteExpiredSessions removes the dead ones.
	DeleteExpiredSessions(ctx context.Context, now time.Time, idle, absolute time.Duration) error

	CreateToken(ctx context.Context, token Token, hash string) error
	// TokenUser returns the token and its owner, and records the use. ErrNotFound if the hash is unknown.
	TokenUser(ctx context.Context, hash string, now time.Time) (Token, User, error)
	// ListTokens returns one user's tokens, or everyone's when userID is empty, oldest first.
	ListTokens(ctx context.Context, userID string) ([]Token, error)
	// DeleteToken removes a token of that user, or of anyone when userID is empty. ErrNotFound if none matched.
	DeleteToken(ctx context.Context, id, userID string) error

	AppendAudit(ctx context.Context, entry AuditEntry) error
	// RecentAudit returns the newest first, up to limit. Only the newest 10,000 are kept.
	RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error)
}

// DeviceOverride is what the owner changed about one device. An empty Name keeps the integration's. A nil Room follows
// config.json; an empty one is no room.
// HomeSettings are the home's own: an empty language or timezone isn't chosen yet, and no location is both nil.
type HomeSettings struct {
	Language  string   `json:"language"`
	Timezone  string   `json:"timezone"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

type DeviceOverride struct {
	ID   string  `json:"id"`
	Name string  `json:"name,omitempty"`
	Room *string `json:"room,omitempty"`
}

// ErrNoRoom means there is no room by that name.
var ErrNoRoom = errors.New("no such room")
