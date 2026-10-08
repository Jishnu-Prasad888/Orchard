package ml

import "testing"

func TestClusterSignatures(t *testing.T) {
	sigs := []FileSignature{
		{FileID: "a", Signature: 0b0000},
		{FileID: "b", Signature: 0b0001}, // distance 1 from a: joins its group
		{FileID: "c", Signature: 0b1111}, // distance 4 from a and b: alone
		{FileID: "d", Signature: 0xFFFFFFFFFFFFFFFF},
	}
	groups := clusterSignatures(sigs, 1)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 (c and d have no close match)", len(groups))
	}
	if got := groups[0].FileIDs; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("group = %v, want [a b]", got)
	}
}

func TestClusterSignaturesChainsThroughAMiddleFile(t *testing.T) {
	// a-b and b-c are each close enough, but a-c alone would not be — single
	// linkage still puts all three in one group via the chain through b.
	sigs := []FileSignature{
		{FileID: "a", Signature: 0b000000},
		{FileID: "b", Signature: 0b000011},
		{FileID: "c", Signature: 0b001111},
	}
	groups := clusterSignatures(sigs, 2)
	if len(groups) != 1 || len(groups[0].FileIDs) != 3 {
		t.Fatalf("groups = %+v, want one group of all three", groups)
	}
}

func TestClusterSignaturesDropsSingletons(t *testing.T) {
	sigs := []FileSignature{
		{FileID: "a", Signature: 0},
		{FileID: "b", Signature: 0xFFFFFFFFFFFFFFFF},
	}
	if groups := clusterSignatures(sigs, 10); len(groups) != 0 {
		t.Errorf("groups = %+v, want none (nothing close enough)", groups)
	}
}

func TestClusterSignaturesIsDeterministic(t *testing.T) {
	sigs := []FileSignature{
		{FileID: "z", Signature: 1},
		{FileID: "a", Signature: 2},
		{FileID: "m", Signature: 3},
	}
	first := clusterSignatures(sigs, 2)
	second := clusterSignatures(sigs, 2)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected one group each run, got %d and %d", len(first), len(second))
	}
	want := []string{"a", "m", "z"}
	for i, ids := range [][]string{first[0].FileIDs, second[0].FileIDs} {
		if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
			t.Errorf("run %d order = %v, want %v", i, ids, want)
		}
	}
}
