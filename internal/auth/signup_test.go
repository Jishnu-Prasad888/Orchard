package auth

import (
	"context"
	"testing"
)

func TestRequestSignup(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	p, err := svc.RequestSignup(ctx, "newperson", "correct-horse-battery", Meta{IPAddress: "127.0.0.1"})
	if err != nil {
		t.Fatalf("RequestSignup: %v", err)
	}
	if p.Username != "newperson" {
		t.Errorf("Username = %q, want %q", p.Username, "newperson")
	}
	if p.ID == "" {
		t.Error("ID should be set")
	}

	pending, err := svc.ListPendingSignups(ctx)
	if err != nil {
		t.Fatalf("ListPendingSignups: %v", err)
	}
	if len(pending) != 1 || pending[0].Username != "newperson" {
		t.Errorf("pending = %+v, want one request for newperson", pending)
	}
}

func TestRequestSignup_InvalidCredentials(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.RequestSignup(context.Background(), "bad username!", "x", Meta{}); err == nil {
		t.Error("expected a validation error for a bad username")
	}
}

func TestRequestSignup_ConflictsWithExistingUser(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc) // creates "admin"

	if _, err := svc.RequestSignup(ctx, "admin", "correct-horse-battery", Meta{}); err != ErrConflict {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestRequestSignup_ConflictsWithAnotherPendingRequest(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.RequestSignup(ctx, "newperson", "correct-horse-battery", Meta{}); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if _, err := svc.RequestSignup(ctx, "newperson", "another-password", Meta{}); err != ErrConflict {
		t.Errorf("second request err = %v, want ErrConflict", err)
	}
}

func TestApprovePendingSignup_CreatesUserAndClearsQueue(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)

	p, err := svc.RequestSignup(ctx, "newperson", "correct-horse-battery", Meta{})
	if err != nil {
		t.Fatalf("RequestSignup: %v", err)
	}

	user, err := svc.ApprovePendingSignup(ctx, p.ID, "user", Meta{})
	if err != nil {
		t.Fatalf("ApprovePendingSignup: %v", err)
	}
	if user.Username != "newperson" || user.Role != RoleUser {
		t.Errorf("approved user = %+v", user)
	}

	// The new account's credentials must actually work.
	if _, _, err := svc.Login(ctx, "newperson", "correct-horse-battery", Meta{}); err != nil {
		t.Errorf("login with approved credentials failed: %v", err)
	}

	pending, err := svc.ListPendingSignups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending signups after approval = %d, want 0", len(pending))
	}
}

func TestApprovePendingSignup_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.ApprovePendingSignup(context.Background(), "ghost", "user", Meta{}); err != ErrSignupNotFound {
		t.Errorf("err = %v, want ErrSignupNotFound", err)
	}
}

func TestDenyPendingSignup(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	p, err := svc.RequestSignup(ctx, "newperson", "correct-horse-battery", Meta{})
	if err != nil {
		t.Fatalf("RequestSignup: %v", err)
	}
	if err := svc.DenyPendingSignup(ctx, p.ID, Meta{}); err != nil {
		t.Fatalf("DenyPendingSignup: %v", err)
	}

	pending, err := svc.ListPendingSignups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending signups after denial = %d, want 0", len(pending))
	}

	// Denial frees the username for a fresh request.
	if _, err := svc.RequestSignup(ctx, "newperson", "correct-horse-battery", Meta{}); err != nil {
		t.Errorf("re-request after denial should succeed, got %v", err)
	}
}

func TestDenyPendingSignup_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.DenyPendingSignup(context.Background(), "ghost", Meta{}); err != ErrSignupNotFound {
		t.Errorf("err = %v, want ErrSignupNotFound", err)
	}
}
