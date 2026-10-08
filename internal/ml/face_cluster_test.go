package ml

import (
	"math"
	"testing"
)

// descOf builds a normalised descriptor from (axis, weight) pairs.
func descOf(parts ...float64) []float32 {
	v := make([]float32, 16)
	for i := 0; i+1 < len(parts); i += 2 {
		v[int(parts[i])] += float32(parts[i+1])
	}
	return l2Normalize(v)
}

// One person seen frontally (p1–p3) and side-on (p4, ~0.5 to each frontal
// face), plus someone who resembles only one of the frontal photos.
func groupingFixture() []clusterFace {
	return []clusterFace{
		{ID: "p1", Desc: descOf(0, 1), Quality: 1, Free: true},
		{ID: "p2", Desc: descOf(0, 1, 1, 0.3), Quality: 3, Free: true},
		{ID: "p3", Desc: descOf(0, 1, 2, 0.3), Quality: 2, Free: true},
		{ID: "p4", Desc: descOf(0, 1, 3, 1.6), Quality: 1, Free: true},
		{ID: "s", Desc: descOf(0, 0.6, 1, 1, 6, 1), Quality: 1, Free: true},
	}
}

func TestPlanGroupsChainsPosesButNotStrangers(t *testing.T) {
	faces := groupingFixture()
	plan := planGroups(faces, nil, nil, 0.45)
	if plan.Created != 1 || len(plan.NewGroups) != 1 {
		t.Fatalf("plan = %+v; want exactly one new person", plan)
	}
	key := newGroupKey(0)
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		if plan.Assign[id] != key {
			t.Errorf("%s -> %q; want the new person", id, plan.Assign[id])
		}
	}
	if plan.Assign["s"] != "" {
		t.Errorf("stranger -> %q; want unassigned (one supporting match is not enough)", plan.Assign["s"])
	}
	if plan.Covers[key] != "p2" {
		t.Errorf("cover = %q; want the best-quality face p2", plan.Covers[key])
	}
}

func TestPlanGroupsKeepsAnchorsAndReusesIDs(t *testing.T) {
	faces := groupingFixture()
	// p1 belongs to "Mom" (named, so an anchor); p2 and p3 sat in an old
	// automatic group "old", which a stricter pass had split from p4.
	faces[0].PersonID, faces[0].Free = "mom", false
	anchors := map[string][]Exemplar{"mom": {{Descriptor: faces[0].Desc, Manual: true}}}
	plan := planGroups(faces, anchors, map[string]bool{"old": true}, 0.45)
	for _, id := range []string{"p2", "p3"} {
		if plan.Assign[id] != "mom" {
			t.Errorf("%s -> %q; want Mom", id, plan.Assign[id])
		}
	}
	if _, moved := plan.Assign["p1"]; moved {
		t.Error("anchored face p1 must not be re-planned")
	}

	// Without the anchor, the old automatic group keeps its id.
	faces = groupingFixture()
	faces[1].PersonID, faces[2].PersonID = "old", "old"
	faces[3].PersonID = "older"
	plan = planGroups(faces, nil, map[string]bool{"old": true, "older": true}, 0.45)
	if plan.Created != 0 || plan.Assign["p4"] != "old" || plan.Assign["p1"] != "old" {
		t.Errorf("plan = %+v; want everyone in the reused group \"old\"", plan)
	}
	if len(plan.Dropped) != 1 || plan.Dropped[0] != "older" {
		t.Errorf("dropped = %v; want [older]", plan.Dropped)
	}
}

func TestGroupingFixtureSimilarities(t *testing.T) {
	f := groupingFixture()
	sim := func(a, b int) float64 { return DescriptorCosine(f[a].Desc, f[b].Desc) }
	if s := sim(3, 0); s < 0.45 || s > 0.6 {
		t.Errorf("side-on vs frontal = %.2f; fixture expects ~0.5", s)
	}
	if s := math.Max(sim(4, 0), sim(4, 2)); s >= 0.45 {
		t.Errorf("stranger matches more than one face (%.2f); fixture is wrong", s)
	}
}

// A person created by hand from one face pulls in the whole automatic group
// that matches them, not just the faces that individually match that one face.
func TestPlanGroupsNewPersonAttractsTheirGroup(t *testing.T) {
	faces := groupingFixture()
	for i := range faces[1:4] {
		faces[1+i].PersonID = "old" // p2, p3, p4 sat in an automatic group
	}
	faces[0].PersonID, faces[0].Free = "asha", false
	anchors := map[string][]Exemplar{"asha": {{Descriptor: faces[0].Desc, Manual: true}}}
	plan := planGroups(faces, anchors, map[string]bool{"old": true}, 0.45)
	for _, id := range []string{"p2", "p3", "p4"} {
		if plan.Assign[id] != "asha" {
			t.Errorf("%s -> %q; want Asha", id, plan.Assign[id])
		}
	}
	if plan.Assign["s"] != "" {
		t.Errorf("stranger -> %q; want unassigned", plan.Assign["s"])
	}
	if len(plan.Dropped) != 1 || plan.Dropped[0] != "old" {
		t.Errorf("dropped = %v; want the emptied automatic group", plan.Dropped)
	}
}

// Two different people never merge, however alike their faces are.
func TestPlanGroupsNeverMergesTwoAnchors(t *testing.T) {
	faces := groupingFixture()
	anchors := map[string][]Exemplar{
		"a": {{Descriptor: faces[0].Desc}},
		"b": {{Descriptor: faces[1].Desc}},
	}
	plan := planGroups(faces[2:3], anchors, nil, 0.45)
	if got := plan.Assign["p3"]; got != "a" && got != "b" {
		t.Errorf("p3 -> %q; want one of the two people", got)
	}
}
