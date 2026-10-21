package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/amir1430/gamify/admin/internal/store"
	"github.com/amir1430/gamify/apptest"
)

func TestRolePerms(t *testing.T) {
	want := map[Role]map[Perm]bool{
		RoleOwner:    {PermView: true, PermIdentity: true, PermLimits: true, PermAdjust: true, PermApprove: true, PermAdmins: true},
		RoleOperator: {PermView: true, PermIdentity: true, PermLimits: true, PermAdjust: true, PermApprove: false, PermAdmins: false},
		RoleSupport:  {PermView: true, PermIdentity: true, PermLimits: false, PermAdjust: false, PermApprove: false, PermAdmins: false},
		RoleViewer:   {PermView: true, PermIdentity: false, PermLimits: false, PermAdjust: false, PermApprove: false, PermAdmins: false},
	}
	n := 0
	for r, perms := range want {
		if !r.Valid() {
			t.Errorf("%s not valid", r)
		}
		for p, ok := range perms {
			n++
			if r.Can(p) != ok {
				t.Errorf("%s.Can(%s) = %v, want %v", r, p, !ok, ok)
			}
		}
	}
	if n != 24 {
		t.Fatalf("%d assertions", n)
	}
	if Role("root").Valid() || Role("root").Can(PermView) || Role("").Can(PermView) {
		t.Fatal("unknown role has permissions")
	}
	if len(rolePerms) != 4 {
		t.Fatalf("%d roles in rolePerms", len(rolePerms))
	}
}

func TestCreateUser(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	pw, err := CreateUser(ctx, a.Pool, "  Owner@Brand.Example ", RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 20 || strings.ContainsAny(pw, "0O1lI") {
		t.Fatalf("password %q", pw)
	}
	u, err := store.New(a.Pool).GetUserByEmail(ctx, "owner@brand.example")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "owner" || !checkPassword(u.PasswordHash, pw) || u.TotpSecret != nil || u.TotpLastStep != 0 {
		t.Fatalf("stored %+v, password matches %v", u, checkPassword(u.PasswordHash, pw))
	}
	if _, err := CreateUser(ctx, a.Pool, "OWNER@brand.example", RoleViewer); err == nil {
		t.Fatal("duplicate email accepted")
	}
	if _, err := CreateUser(ctx, a.Pool, "x@brand.example", Role("root")); err == nil {
		t.Fatal("bad role accepted")
	}
	for _, bad := range []string{"", "   ", "nobody"} {
		if _, err := CreateUser(ctx, a.Pool, bad, RoleViewer); err == nil {
			t.Fatalf("email %q accepted", bad)
		}
	}
	pw2, err := CreateUser(ctx, a.Pool, "viewer@brand.example", RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	if pw2 == pw {
		t.Fatal("same password twice")
	}
}
