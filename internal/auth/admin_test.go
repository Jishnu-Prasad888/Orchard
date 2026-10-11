package auth

import (
	"context"
	"testing"
)

func TestUpdateUserRole_Demote(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	admin, _ := mustBootstrap(t, svc)
	member, err := svc.CreateUser(ctx, "member", "correct-horse-battery", "admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Two admins exist, so demoting one is fine.
	if err := svc.UpdateUserRole(ctx, member.ID, "user", Meta{}); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	got, err := svc.UserByID(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleUser {
		t.Errorf("Role = %q, want %q", got.Role, RoleUser)
	}
	_ = admin
}

func TestUpdateUserRole_LastAdminRejected(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	admin, _ := mustBootstrap(t, svc)

	if err := svc.UpdateUserRole(ctx, admin.ID, "user", Meta{}); err != ErrLastAdmin {
		t.Errorf("UpdateUserRole(last admin) err = %v, want ErrLastAdmin", err)
	}
	got, err := svc.UserByID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleAdmin {
		t.Error("role must be unchanged after a rejected demotion")
	}
}

func TestUpdateUserRole_UnknownUser(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.UpdateUserRole(context.Background(), "ghost", "admin", Meta{}); err != ErrUserNotFound {
		t.Errorf("err = %v, want ErrUserNotFound", err)
	}
}

func TestSetUserEnabled_DisableRevokesSessions(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)
	member, err := svc.CreateUser(ctx, "member", "correct-horse-battery", "user")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, token := mustLoginUser(t, svc, "member", "correct-horse-battery")

	if err := svc.SetUserEnabled(ctx, member.ID, false, Meta{}); err != nil {
		t.Fatalf("SetUserEnabled: %v", err)
	}

	if _, err := svc.Authenticate(ctx, token); err != ErrUnauthorized {
		t.Errorf("Authenticate after disable err = %v, want ErrUnauthorized", err)
	}

	var n int
	if err := pool.QueryRow(`SELECT is_enabled FROM users WHERE id = ?`, member.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("is_enabled should be 0 after disabling")
	}
}

func TestSetUserEnabled_LastAdminRejected(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	admin, _ := mustBootstrap(t, svc)

	if err := svc.SetUserEnabled(ctx, admin.ID, false, Meta{}); err != ErrLastAdmin {
		t.Errorf("err = %v, want ErrLastAdmin", err)
	}
}

func TestSetUserEnabled_SecondAdminCanBeDisabled(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)
	second, err := svc.CreateUser(ctx, "second-admin", "correct-horse-battery", "admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := svc.SetUserEnabled(ctx, second.ID, false, Meta{}); err != nil {
		t.Errorf("disabling a non-last admin should succeed, got %v", err)
	}
}

func TestDeleteUser_RemovesAccountAndSessions(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)
	member, err := svc.CreateUser(ctx, "member", "correct-horse-battery", "user")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	mustLoginUser(t, svc, "member", "correct-horse-battery")

	if err := svc.DeleteUser(ctx, member.ID, Meta{}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, err := svc.UserByID(ctx, member.ID); err == nil {
		t.Error("user should no longer exist")
	}
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, member.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sessions for deleted user = %d, want 0 (cascade delete)", n)
	}
}

func TestDeleteUser_LastAdminRejected(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	admin, _ := mustBootstrap(t, svc)

	if err := svc.DeleteUser(ctx, admin.ID, Meta{}); err != ErrLastAdmin {
		t.Errorf("err = %v, want ErrLastAdmin", err)
	}
	if _, err := svc.UserByID(ctx, admin.ID); err != nil {
		t.Error("admin must still exist after a rejected delete")
	}
}

func TestDeleteUser_UnknownUser(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.DeleteUser(context.Background(), "ghost", Meta{}); err != ErrUserNotFound {
		t.Errorf("err = %v, want ErrUserNotFound", err)
	}
}
