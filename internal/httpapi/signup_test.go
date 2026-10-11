package httpapi

import (
	"net/http"
	"testing"
)

func TestUpdateUser_RoleAndEnabled(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"alice","password":"s3cret-s3cret","role":"user"}`)
	jsonUnmarshalRec(t, rec, &created)

	rec = client.roundTrip(t, http.MethodPatch, "/api/v1/users/"+created.User.ID, `{"role":"admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch role status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var updated struct {
		User struct {
			Role    string `json:"role"`
			Enabled bool   `json:"enabled"`
		} `json:"user"`
	}
	jsonUnmarshalRec(t, rec, &updated)
	if updated.User.Role != "admin" {
		t.Errorf("role = %q, want admin", updated.User.Role)
	}

	rec = client.roundTrip(t, http.MethodPatch, "/api/v1/users/"+created.User.ID, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch enabled status = %d, body=%s", rec.Code, rec.Body.String())
	}
	jsonUnmarshalRec(t, rec, &updated)
	if updated.User.Enabled {
		t.Error("expected enabled=false after disabling")
	}
}

func TestUpdateUser_LastAdminProtected(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	rec := client.roundTrip(t, http.MethodGet, "/api/v1/auth/me", "")
	jsonUnmarshalRec(t, rec, &me)

	rec = client.roundTrip(t, http.MethodPatch, "/api/v1/users/"+me.User.ID, `{"role":"user"}`)
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)

	rec = client.roundTrip(t, http.MethodPatch, "/api/v1/users/"+me.User.ID, `{"enabled":false}`)
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)
}

func TestDeleteUser_Endpoint(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"alice","password":"s3cret-s3cret","role":"user"}`)
	jsonUnmarshalRec(t, rec, &created)

	rec = client.roundTrip(t, http.MethodDelete, "/api/v1/users/"+created.User.ID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/users", "")
	var list struct {
		Users []struct {
			ID string `json:"id"`
		} `json:"users"`
	}
	jsonUnmarshalRec(t, rec, &list)
	for _, u := range list.Users {
		if u.ID == created.User.ID {
			t.Error("deleted user still present in user list")
		}
	}
}

func TestDeleteUser_LastAdminProtected(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	rec := client.roundTrip(t, http.MethodGet, "/api/v1/auth/me", "")
	jsonUnmarshalRec(t, rec, &me)

	rec = client.roundTrip(t, http.MethodDelete, "/api/v1/users/"+me.User.ID, "")
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)
}

func TestSignupRequests_DisabledByDefault(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)
	client.dropSession()

	rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/signup-requests",
		`{"username":"newperson","password":"correct-horse-battery"}`)
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
}

func TestSignupRequests_FullLifecycle(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	// Admin turns the setting on.
	rec := client.roundTrip(t, http.MethodPut, "/api/v1/settings/signup-requests", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable signup requests status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// The public status endpoint reflects it.
	client.dropSession()
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/auth/status", "")
	var status struct {
		SignupRequestsEnabled bool `json:"signup_requests_enabled"`
	}
	jsonUnmarshalRec(t, rec, &status)
	if !status.SignupRequestsEnabled {
		t.Fatal("expected signup_requests_enabled=true in /auth/status")
	}

	// An anonymous visitor requests an account.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/auth/signup-requests",
		`{"username":"newperson","password":"correct-horse-battery"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("request signup status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Request struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"request"`
	}
	jsonUnmarshalRec(t, rec, &created)
	if created.Request.Username != "newperson" {
		t.Fatalf("request = %+v", created.Request)
	}

	// Signing back in as admin to manage the request.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct-horse-battery"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/signup-requests", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list pending status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	jsonUnmarshalRec(t, rec, &list)
	if len(list.Requests) != 1 || list.Requests[0].ID != created.Request.ID {
		t.Fatalf("pending list = %+v", list.Requests)
	}

	// Approve it.
	rec = client.roundTrip(t, http.MethodPost,
		"/api/v1/signup-requests/"+created.Request.ID+"/approve", `{"role":"user"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("approve status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// The queue is now empty.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/signup-requests", "")
	jsonUnmarshalRec(t, rec, &list)
	if len(list.Requests) != 0 {
		t.Errorf("pending list after approval = %+v, want empty", list.Requests)
	}

	// The approved account can log in.
	client.dropSession()
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"newperson","password":"correct-horse-battery"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("new user login status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSignupRequests_Deny(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)
	client.roundTrip(t, http.MethodPut, "/api/v1/settings/signup-requests", `{"enabled":true}`)
	client.dropSession()

	rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/signup-requests",
		`{"username":"newperson","password":"correct-horse-battery"}`)
	var created struct {
		Request struct {
			ID string `json:"id"`
		} `json:"request"`
	}
	jsonUnmarshalRec(t, rec, &created)

	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct-horse-battery"}`)

	rec = client.roundTrip(t, http.MethodPost, "/api/v1/signup-requests/"+created.Request.ID+"/deny", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deny status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Denial frees the username.
	client.dropSession()
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/auth/signup-requests",
		`{"username":"newperson","password":"correct-horse-battery"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-request status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSignupRequests_NonAdminCannotManage(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)
	client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"bob","password":"s3cret-s3cret","role":"user"}`)
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/logout", "")
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"bob","password":"s3cret-s3cret"}`)

	for _, req := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/signup-requests", ""},
		{http.MethodPost, "/api/v1/signup-requests/x/approve", ""},
		{http.MethodPost, "/api/v1/signup-requests/x/deny", ""},
		{http.MethodPut, "/api/v1/settings/signup-requests", `{"enabled":true}`},
	} {
		rec := client.roundTrip(t, req.method, req.path, req.body)
		assertErrorEnvelope(t, rec, http.StatusForbidden, CodeForbidden)
	}
}
