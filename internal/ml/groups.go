package ml

import "sort"

// SimilarityGroup is a set of present photos connected by a chain of pairwise
// perceptual distances, each at or below the configured threshold — the same
// single-linkage rule Similar applies to one file at a time, run once across
// the whole library.
type SimilarityGroup struct {
	FileIDs []string
}

// clusterSignatures groups signatures into connected components: two files
// land in the same group when some chain of pairwise Hamming distances, each
// no more than threshold, connects them. A personal library of a few thousand
// photos is a few million comparisons — instant with a 64-bit popcount, and
// simple beats a spatial index at this scale.
//
// Groups of one are dropped; there is nothing to review there. Both the file
// IDs within a group and the groups themselves are sorted, so a re-run over
// an unchanged library returns the identical answer.
func clusterSignatures(sigs []FileSignature, threshold int) []SimilarityGroup {
	parent := make(map[string]string, len(sigs))
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for _, s := range sigs {
		parent[s.FileID] = s.FileID
	}
	for i := 0; i < len(sigs); i++ {
		for j := i + 1; j < len(sigs); j++ {
			if popcount(sigs[i].Signature^sigs[j].Signature) <= threshold {
				union(sigs[i].FileID, sigs[j].FileID)
			}
		}
	}

	byRoot := make(map[string][]string)
	for _, s := range sigs {
		r := find(s.FileID)
		byRoot[r] = append(byRoot[r], s.FileID)
	}
	var groups []SimilarityGroup
	for _, ids := range byRoot {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		groups = append(groups, SimilarityGroup{FileIDs: ids})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].FileIDs[0] < groups[j].FileIDs[0] })
	return groups
}
