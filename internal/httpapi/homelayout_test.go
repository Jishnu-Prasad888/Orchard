package httpapi

import (
	"net/http"
	"testing"
)

func TestHomeLayout_DefaultsThenRoundTrips(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet, "/api/v1/settings/home-layout", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Sections []struct {
			ID      string `json:"id"`
			Visible bool   `json:"visible"`
		} `json:"sections"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Sections) == 0 {
		t.Fatalf("expected a default layout, got none")
	}
	if body.Sections[0].ID != "continue-reading" || !body.Sections[0].Visible {
		t.Fatalf("first default section = %+v, want visible continue-reading first", body.Sections[0])
	}

	reordered := []map[string]any{
		{"id": "favorites", "visible": false},
		{"id": "continue-reading", "visible": true},
	}
	rec = client.do(t, http.MethodPut, "/api/v1/settings/home-layout",
		map[string]any{"sections": reordered})
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body=%s", rec.Code, rec.Body.String())
	}
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Sections) < 2 || body.Sections[0].ID != "favorites" || body.Sections[0].Visible {
		t.Fatalf("saved layout = %+v, want hidden favorites first", body.Sections)
	}
	if body.Sections[1].ID != "continue-reading" || !body.Sections[1].Visible {
		t.Fatalf("saved layout = %+v, want continue-reading second", body.Sections)
	}

	rec = client.do(t, http.MethodGet, "/api/v1/settings/home-layout", nil)
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Sections) < 2 || body.Sections[0].ID != "favorites" || body.Sections[0].Visible {
		t.Fatalf("reloaded layout = %+v, want the saved order to persist", body.Sections)
	}
}

func TestHomeLayout_DropsUnknownSectionIDs(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPut, "/api/v1/settings/home-layout",
		map[string]any{"sections": []map[string]any{{"id": "not-a-real-section", "visible": true}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Sections []struct {
			ID string `json:"id"`
		} `json:"sections"`
	}
	jsonUnmarshalRec(t, rec, &body)
	for _, s := range body.Sections {
		if s.ID == "not-a-real-section" {
			t.Fatalf("unknown section id survived: %+v", body.Sections)
		}
	}
}
