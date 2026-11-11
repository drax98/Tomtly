// Package auth holds the roles, password hashing, secrets and login throttle behind the API's access control.
package auth

import "slices"

// Permissions, as the API and the page name them.
const (
	DevicesRead        = "devices.read"
	DevicesWrite       = "devices.write"
	AutomationsRead    = "automations.read"
	AutomationsEdit    = "automations.edit"
	AutomationsLive    = "automations.live"
	AutomationsApprove = "automations.approve"
	ExtensionsRead     = "extensions.read"
	ExtensionsManage   = "extensions.manage"
	UsersManage        = "users.manage"
	AuditRead          = "audit.read"
	SettingsManage     = "settings.manage"
)

// Roles lists the roles from least to most powerful.
func Roles() []string { return []string{"viewer", "operator", "editor", "admin"} }

var (
	viewer   = []string{DevicesRead, AutomationsRead, ExtensionsRead}
	operator = append(slices.Clone(viewer), DevicesWrite)
	editor   = append(slices.Clone(operator), AutomationsEdit, AutomationsLive)
	admin    = append(slices.Clone(editor), AutomationsApprove, ExtensionsManage, UsersManage, AuditRead, SettingsManage)
)

// Permissions returns what a role may do; an unknown role may do nothing.
func Permissions(role string) []string {
	switch role {
	case "viewer":
		return slices.Clone(viewer)
	case "operator":
		return slices.Clone(operator)
	case "editor":
		return slices.Clone(editor)
	case "admin":
		return slices.Clone(admin)
	}
	return []string{}
}

// Valid reports whether role is one of Roles.
func Valid(role string) bool { return slices.Contains(Roles(), role) }

// Has reports whether the role grants the permission.
func Has(role, permission string) bool { return slices.Contains(Permissions(role), permission) }

// AtMost reports whether role grants no more than limit does. Unknown roles are never within a limit.
func AtMost(role, limit string) bool {
	roles := Roles()
	i, j := slices.Index(roles, role), slices.Index(roles, limit)
	return i >= 0 && j >= 0 && i <= j
}
