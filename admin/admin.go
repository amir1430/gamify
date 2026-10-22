// Package admin is the opt-in back office: console users with roles, password + TOTP login, an
// append-only audit log and balance adjustments. It has its own tables (goose table goose_admin)
// and listener; a brand turns it on by registering it.
package admin

import (
	"embed"
	"io/fs"
	"net/netip"
	"slices"

	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/ledger"
)

// Migrations are the module's tables: admin_users, admin_sessions, admin_audit,
// admin_adjustments (goose table goose_admin).
//
//go:embed migrations/*.sql
var Migrations embed.FS

const gooseTable = "goose_admin"

// Brand is how the console looks.
type Brand struct {
	Name   string
	Logo   []byte // PNG or SVG bytes
	Accent string // CSS color, e.g. "#7c3aed"
}

// Config turns the console on.
type Config struct {
	Addr     string         // its own listener, never App.HTTP; required
	Origin   string         // the one origin allowed to call the API
	AllowIPs []netip.Prefix // optional: only these clients

	TOTPKey          []byte // 32 bytes, seals TOTP secrets (AES-256-GCM)
	TelegramBotToken string // optional: Telegram Login Widget login

	Brand    Brand
	Fairness *fairness.Service // optional: shows seed pairs (revealed only)

	ApproveAbove map[ledger.Currency]int64 // adjustments above need a second admin
	UI           fs.FS                     // nil = embedded

	// Perms are the brand's own permissions for its views and actions (View), with the roles
	// that hold each, e.g. {"crash.void": {RoleOperator}}. Owners hold every permission. A key
	// can't be empty or a built-in permission's name.
	Perms map[Perm][]Role
}

// Role is a console user's role.
type Role string

const (
	RoleOwner    Role = "owner"
	RoleOperator Role = "operator"
	RoleSupport  Role = "support"
	RoleViewer   Role = "viewer"
)

// Perm is what a handler needs.
type Perm string

const (
	PermView     Perm = "view"     // dashboard, players, activity, limits, audit
	PermIdentity Perm = "identity" // unlink an identity, kick a session
	PermLimits   Perm = "limits"   // change game limits, turn games off
	PermAdjust   Perm = "adjust"   // request a balance adjustment
	PermApprove  Perm = "approve"  // approve an adjustment (not their own)
	PermAdmins   Perm = "admins"   // manage console users, view their sessions

	PermMatchCancel  Perm = "matchmaking.cancel" // matchmakingadmin: cancel a match
	PermMatchReport  Perm = "matchmaking.report" // matchmakingadmin: report a match's result
	PermPartyDisband Perm = "party.disband"      // partyadmin: disband a party
)

var rolePerms = map[Role][]Perm{
	RoleOwner:    {PermView, PermIdentity, PermLimits, PermAdjust, PermApprove, PermAdmins, PermMatchCancel, PermMatchReport, PermPartyDisband},
	RoleOperator: {PermView, PermIdentity, PermLimits, PermAdjust, PermMatchCancel, PermMatchReport, PermPartyDisband},
	RoleSupport:  {PermView, PermIdentity},
	RoleViewer:   {PermView},
}

// Valid reports whether r is one of the four roles.
func (r Role) Valid() bool {
	_, ok := rolePerms[r]
	return ok
}

// builtin reports whether p is one of the built-in permissions.
func builtin(p Perm) bool {
	for _, q := range rolePerms[RoleOwner] {
		if q == p {
			return true
		}
	}
	return false
}

// Can reports whether r has the built-in permission p (brand permissions aside: the console
// checks those against Config.Perms). An unknown role has nothing.
func (r Role) Can(p Perm) bool {
	for _, q := range rolePerms[r] {
		if q == p {
			return true
		}
	}
	return false
}

// can reports whether r holds p: owners hold every permission, built-ins come from the role,
// brand permissions from Config.Perms. An unknown role has nothing.
func (adm *Admin) can(r Role, p Perm) bool {
	switch {
	case !r.Valid():
		return false
	case r == RoleOwner, r.Can(p):
		return true
	}
	return slices.Contains(adm.cfg.Perms[p], r)
}

// knownPerm reports whether p is a built-in or a Config.Perms permission.
func (adm *Admin) knownPerm(p Perm) bool {
	_, ok := adm.cfg.Perms[p]
	return builtin(p) || ok
}

// perms lists what r holds, built-in and brand, sorted.
func (adm *Admin) perms(r Role) []string {
	out := []string{}
	for _, p := range rolePerms[r] {
		out = append(out, string(p))
	}
	for p := range adm.cfg.Perms {
		if adm.can(r, p) {
			out = append(out, string(p))
		}
	}
	slices.Sort(out)
	return out
}
