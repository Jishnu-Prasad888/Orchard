package authz

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Evaluation policy (ADR-0005): most-specific-wins with deny-first tiebreak at
// equal specificity.
//
//   - For a target resource key, only grants whose key is a prefix-or-equal of
//     the target apply (so a folder grant covers its descendants, and only the
//     library grant covers the whole library).
//   - For each capability, walk the matching grants from most specific (deepest
//     key) to least specific (the library). The first grant that mentions the
//     capability decides it; an allow grants, a deny revokes.
//   - At equal specificity a deny beats an allow, so revoking a subtree with an
//     explicit deny wins over an inherited allow on the same key.
//   - Anything not explicitly granted is denied by default.

// covers reports whether ancestorKey is a prefix-or-equal of resourceKey at a
// "/" boundary.
func covers(ancestorKey, resourceKey string) bool {
	if ancestorKey == resourceKey {
		return true
	}
	return strings.HasPrefix(resourceKey, ancestorKey+keySep)
}

// Principal is an authenticated actor evaluated by the service. Admin is the
// account-level convenience mapping: an admin holds every capability on every
// resource. It is evaluated here, in exactly one place in the subsystem.
type Principal struct {
	UserID string
	Admin  bool
}

// Can reports whether the principal may perform all the given capabilities on
// the resource at key. Cached under the service generation.
func (s *Service) Can(ctx context.Context, p Principal, key string, caps ...Capability) (bool, error) {
	if p.Admin {
		return true, nil
	}
	eff, err := s.Effective(ctx, p.UserID, key)
	if err != nil {
		return false, err
	}
	return eff.All(caps...), nil
}

// Effective returns the capability set the user effectively holds on key.
func (s *Service) Effective(ctx context.Context, userID, key string) (CapSet, error) {
	gen := s.currentGeneration()

	s.mu.RLock()
	if d, ok := s.decides[decisionCacheKey{userID, key}]; ok && d.generation == gen {
		caps := cloneCaps(d.caps)
		s.mu.RUnlock()
		return caps, nil
	}
	s.mu.RUnlock()

	grants, err := s.grantsFor(ctx, userID)
	if err != nil {
		return nil, err
	}

	type match struct {
		depth int
		cap   Capability
		eff   Effect
	}
	var matches []match
	for _, g := range grants {
		if !covers(g.ResourceKey, key) {
			continue
		}
		depth := len(strings.Split(g.ResourceKey, keySep))
		for _, c := range AllCapabilities {
			if g.Capabilities[c] {
				matches = append(matches, match{depth: depth, cap: c, eff: g.Effect})
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].depth != matches[j].depth {
			return matches[i].depth > matches[j].depth // most specific first
		}
		return matches[i].eff == EffectDeny // deny beats allow at equal depth
	})

	caps := make(CapSet)
	seen := make(map[Capability]bool)
	for _, m := range matches {
		if seen[m.cap] {
			continue
		}
		seen[m.cap] = true
		if m.eff == EffectAllow {
			caps[m.cap] = true
		}
	}

	s.mu.Lock()
	s.decides[decisionCacheKey{userID, key}] = decision{caps: cloneCaps(caps), generation: gen}
	s.mu.Unlock()
	return caps, nil
}

// grantsFor returns the persisted grants for a user, cached per generation.
func (s *Service) grantsFor(ctx context.Context, userID string) ([]grant, error) {
	gen := s.currentGeneration()

	s.mu.RLock()
	entry, ok := s.grants[grantCacheKey{userID}]
	s.mu.RUnlock()
	if ok && entry.generation == gen {
		return entry.grants, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, resource_key, capabilities, effect, created_at
		 FROM permission_grants WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("query grants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var grants []grant
	for rows.Next() {
		var (
			g         grant
			capsCSV   string
			effectStr string
			createdAt string
		)
		if err := rows.Scan(&g.ID, &g.UserID, &g.ResourceKey, &capsCSV, &effectStr, &createdAt); err != nil {
			return nil, fmt.Errorf("scan grant: %w", err)
		}
		set, err := capSetFromStrings(strings.Split(capsCSV, ","))
		if err != nil {
			return nil, fmt.Errorf("corrupt capabilities %q: %w", capsCSV, err)
		}
		g.Capabilities = set
		g.Effect = Effect(effectStr)
		if g.Effect != EffectAllow && g.Effect != EffectDeny {
			return nil, fmt.Errorf("corrupt grant effect %q", effectStr)
		}
		if g.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grants: %w", err)
	}

	s.mu.Lock()
	s.grants[grantCacheKey{userID}] = cachedGrants{generation: gen, grants: grants}
	s.mu.Unlock()
	return grants, nil
}

func cloneCaps(in CapSet) CapSet {
	if in == nil {
		return nil
	}
	out := make(CapSet, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// currentGeneration returns the service generation. All grant and share
// mutations bump it, invalidating cached decisions exactly.
func (s *Service) currentGeneration() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation
}

// bumpGeneration invalidates every cached decision and grant list.
func (s *Service) bumpGeneration() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.grants = make(map[grantCacheKey]cachedGrants)
	s.decides = make(map[decisionCacheKey]decision)
}
