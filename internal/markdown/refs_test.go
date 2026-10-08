package markdown

import "testing"

func TestParseReferences_Basic(t *testing.T) {
	body := "Went to the beach — [[media:abc123|sunset photo]] was gorgeous.\n\nThen [[memory:mem9]] reminded me."
	refs := ParseReferences(body)

	if len(refs) != 2 {
		t.Fatalf("len = %d, want 2", len(refs))
	}

	r0 := refs[0]
	if r0.Type != RefMedia || r0.ID != "abc123" || r0.Label != "sunset photo" {
		t.Errorf("first ref = %+v, want media/abc123/label", r0)
	}
	if r0.Raw != "[[media:abc123|sunset photo]]" {
		t.Errorf("raw = %q", r0.Raw)
	}

	r1 := refs[1]
	if r1.Type != RefMemory || r1.ID != "mem9" || r1.Label != "" {
		t.Errorf("second ref = %+v, want memory/mem9/no label", r1)
	}
}

func TestParseReferences_AllTypes(t *testing.T) {
	body := "[[media:f1]] [[memory:m1]] [[album:a1]] [[person:p1]] [[tag:t1]]"
	refs := ParseReferences(body)
	if len(refs) != 5 {
		t.Fatalf("len = %d, want 5", len(refs))
	}
	want := []RefType{RefMedia, RefMemory, RefAlbum, RefPerson, RefTag}
	for i := range refs {
		if refs[i].Type != want[i] {
			t.Errorf("refs[%d].Type = %q, want %q", i, refs[i].Type, want[i])
		}
	}
}

func TestParseReferences_IgnoresMalformed(t *testing.T) {
	refs := ParseReferences("plain text [[nope]] [[UNKNOWN:id]] [[media:]] [[media:id|x\n")
	if len(refs) != 0 {
		t.Errorf("expected no refs, got %+v", refs)
	}
}

func TestParseReferences_Offsets(t *testing.T) {
	body := "hello [[media:id]] world"
	refs := ParseReferences(body)
	if len(refs) != 1 {
		t.Fatalf("len = %d, want 1", len(refs))
	}
	if refs[0].Start != 6 || refs[0].End != len("[[media:id]]")+6 {
		t.Errorf("offsets = [%d,%d)", refs[0].Start, refs[0].End)
	}
}

func TestParseReferences_NilOnEmpty(t *testing.T) {
	if refs := ParseReferences(""); refs != nil {
		t.Errorf("expected nil for empty body, got %v", refs)
	}
}
