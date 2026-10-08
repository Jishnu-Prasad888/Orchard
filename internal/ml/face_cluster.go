package ml

import (
	"sort"
	"strconv"
)

// Grouping faces into people.
//
// Every grouping pass re-plans the automatic part of the library from scratch
// rather than placing faces one at a time in arrival order. Placing faces
// greedily made the result depend on which photo happened to be read first:
// the first face of someone seen side-on, or without their glasses, started a
// new "Person N" and was never reconsidered.
//
// What a person decided is kept: anyone with a chosen name or with a face a
// human assigned is an anchor, and their faces are never moved. Faces a human
// took off a person ("Not …") are held aside and never grouped automatically.
// Everything else (unassigned faces, and faces in untouched "Person N"
// groups) is free and is grouped again.
//
// One agglomerative clustering does the work. Each anchor enters it as a
// group already formed from its faces (hand-confirmed first, up to
// maxExemplars), each free face as a group of one. Pairs of faces are taken
// strongest first, and two groups merge once enough pairs between them reach
// the threshold: one when both groups are a single face, two otherwise.
// Requiring two supporting pairs is what lets a person's frontal,
// three-quarter and glasses-off faces chain together without one odd look
// pulling a stranger in. Two different anchors never merge.
//
// So a person created by hand from a single face immediately attracts the
// faces that match it, and a whole automatic group that matches them moves
// across together rather than face by face.
//
// Afterwards, groups holding an anchor give their free faces to that person.
// Other groups of two or more faces become people, reusing the id (and so the
// name, cover and links) of the old automatic group they overlap most. A lone
// face stays unassigned under "Who is this?" until a second photo of that
// person arrives or someone places it by hand; otherwise every passer-by in
// the background would become a person.

// clusterFace is one face as seen by the planner.
type clusterFace struct {
	ID       string
	Desc     []float32
	Quality  float64 // cover preference: larger, more confident faces win
	PersonID string  // current assignment ("" when unassigned)
	Free     bool    // may be (re)grouped
}

// groupPlan is the outcome of planGroups.
type groupPlan struct {
	// Assign maps every free face to its person ("" = unassigned). Faces of a
	// group in NewGroups map to "new:<index>".
	Assign map[string]string
	// NewGroups lists the face ids of clusters that need a new person.
	NewGroups [][]string
	// Covers suggests a cover face for every reused or new person whose
	// current cover may no longer be one of its faces.
	Covers map[string]string
	// Dropped lists automatic people that no longer have any face.
	Dropped []string
	// Inspected / Assigned / Created summarise the plan.
	Inspected, Assigned, Created int
}

// newGroupKey names the i-th new group inside a groupPlan.
func newGroupKey(i int) string { return "new:" + strconv.Itoa(i) }

// clusterNode is one face in the clustering: a free face (face >= 0, an index
// into faces) or an anchor's exemplar (face < 0, label set).
type clusterNode struct {
	desc  []float32
	face  int
	label string
}

// planGroups computes the grouping. anchors are the people whose faces are
// fixed (their exemplars, hand-confirmed first); autoPeople are the untouched
// automatic groups whose faces are free.
func planGroups(faces []clusterFace, anchors map[string][]Exemplar, autoPeople map[string]bool, threshold float64) *groupPlan {
	plan := &groupPlan{Assign: map[string]string{}, Covers: map[string]string{}}

	var nodes []clusterNode
	for i, f := range faces {
		if f.Free {
			plan.Inspected++
			nodes = append(nodes, clusterNode{desc: f.Desc, face: i})
		}
	}
	anchorIDs := make([]string, 0, len(anchors))
	for pid := range anchors {
		anchorIDs = append(anchorIDs, pid)
	}
	sort.Strings(anchorIDs)
	for _, pid := range anchorIDs {
		ex := anchors[pid]
		if len(ex) > maxExemplars {
			ex = ex[:maxExemplars]
		}
		for _, e := range ex {
			nodes = append(nodes, clusterNode{desc: e.Descriptor, face: -1, label: pid})
		}
	}

	type candidate struct {
		members []int          // indexes into faces
		old     map[string]int // old automatic person -> faces it held
	}
	var cands []*candidate
	for _, group := range clusterNodes(nodes, threshold) {
		label := ""
		var members []int
		for _, n := range group {
			if nodes[n].face < 0 {
				label = nodes[n].label
			} else {
				members = append(members, nodes[n].face)
			}
		}
		switch {
		case len(members) == 0:
			continue
		case label != "":
			for _, i := range members {
				plan.Assign[faces[i].ID] = label
				if faces[i].PersonID != label {
					plan.Assigned++
				}
			}
		case len(members) == 1:
			plan.Assign[faces[members[0]].ID] = ""
		default:
			c := &candidate{members: members, old: map[string]int{}}
			for _, i := range members {
				if pid := faces[i].PersonID; pid != "" && autoPeople[pid] {
					c.old[pid]++
				}
			}
			cands = append(cands, c)
		}
	}

	// Reuse old automatic ids by overlap, biggest overlaps first, so a group
	// that merely gained or lost a face keeps its identity.
	type pairing struct {
		cand int
		pid  string
		n    int
	}
	var pairs []pairing
	for ci, c := range cands {
		for pid, n := range c.old {
			pairs = append(pairs, pairing{ci, pid, n})
		}
	}
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a].n != pairs[b].n {
			return pairs[a].n > pairs[b].n
		}
		if pairs[a].pid != pairs[b].pid {
			return pairs[a].pid < pairs[b].pid
		}
		return pairs[a].cand < pairs[b].cand
	})
	target := make([]string, len(cands))
	used := map[string]bool{}
	for _, p := range pairs {
		if target[p.cand] != "" || used[p.pid] {
			continue
		}
		target[p.cand] = p.pid
		used[p.pid] = true
	}
	for ci, c := range cands {
		key := target[ci]
		if key == "" {
			key = newGroupKey(len(plan.NewGroups))
			ids := make([]string, len(c.members))
			for k, i := range c.members {
				ids[k] = faces[i].ID
			}
			plan.NewGroups = append(plan.NewGroups, ids)
			plan.Created++
		} else {
			for _, i := range c.members {
				if faces[i].PersonID != key {
					plan.Assigned++
				}
			}
		}
		cover := c.members[0]
		for _, i := range c.members {
			if faces[i].Quality > faces[cover].Quality {
				cover = i
			}
		}
		plan.Covers[key] = faces[cover].ID
		for _, i := range c.members {
			plan.Assign[faces[i].ID] = key
		}
	}

	for pid := range autoPeople {
		if !used[pid] {
			plan.Dropped = append(plan.Dropped, pid)
		}
	}
	sort.Strings(plan.Dropped)
	return plan
}

// clusterNodes groups nodes agglomeratively (see the file comment) and returns
// the groups as lists of node indexes, in a stable order. Nodes sharing a
// label start in one group; groups with different labels never merge.
func clusterNodes(nodes []clusterNode, threshold float64) [][]int {
	n := len(nodes)
	parent := make([]int, n)
	members := make([][]int, n)
	label := make([]string, n)
	for i := range parent {
		parent[i] = i
		members[i] = []int{i}
		label[i] = nodes[i].label
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(ra, rb int) int {
		if len(members[ra]) < len(members[rb]) {
			ra, rb = rb, ra
		}
		parent[rb] = ra
		members[ra] = append(members[ra], members[rb]...)
		members[rb] = nil
		if label[ra] == "" {
			label[ra] = label[rb]
		}
		return ra
	}
	// An anchor's faces are one group from the start.
	first := map[string]int{}
	for i, node := range nodes {
		if node.label == "" {
			continue
		}
		if r, ok := first[node.label]; ok {
			first[node.label] = union(find(r), find(i))
		} else {
			first[node.label] = i
		}
	}

	type edge struct {
		a, b int
		sim  float64
	}
	var edges []edge
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			if nodes[a].label != "" && nodes[b].label != "" {
				continue // anchor faces are already placed
			}
			if sim := DescriptorCosine(nodes[a].desc, nodes[b].desc); sim >= threshold {
				edges = append(edges, edge{a, b, sim})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].sim != edges[j].sim {
			return edges[i].sim > edges[j].sim
		}
		if edges[i].a != edges[j].a {
			return edges[i].a < edges[j].a
		}
		return edges[i].b < edges[j].b
	})

	support := make([]map[int]int, n) // group root -> other root -> matching pairs
	for _, e := range edges {
		ra, rb := find(e.a), find(e.b)
		if ra == rb || (label[ra] != "" && label[rb] != "") {
			continue
		}
		if support[ra] == nil {
			support[ra] = map[int]int{}
		}
		if support[rb] == nil {
			support[rb] = map[int]int{}
		}
		support[ra][rb]++
		support[rb][ra]++
		need := 2
		if len(members[ra]) == 1 && len(members[rb]) == 1 {
			need = 1
		}
		if support[ra][rb] < need {
			continue
		}
		keep := union(ra, rb)
		gone := ra
		if keep == ra {
			gone = rb
		}
		delete(support[keep], gone)
		for other, cnt := range support[gone] {
			if other == keep {
				continue
			}
			support[keep][other] += cnt
			support[other][keep] += cnt
			delete(support[other], gone)
		}
		support[gone] = nil
	}

	var out [][]int
	for i := 0; i < n; i++ {
		if find(i) != i {
			continue
		}
		ms := append([]int(nil), members[i]...)
		sort.Ints(ms)
		out = append(out, ms)
	}
	sort.Slice(out, func(a, b int) bool { return out[a][0] < out[b][0] })
	return out
}
