// Package tier maps token scopes to rate-limit tiers.
package tier

import "sync"

// ScopePrefix marks scopes that name a tier: "tier:<name>".
const ScopePrefix = "tier:"

// Tier is an immutable rate-limit tier definition.
type Tier struct {
	Name string
	Rank int64
	T    int64
	B    int64
}

// Registry holds registered tiers.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]Tier
	byRank map[int64]Tier
	min    *Tier
}

// NewRegistry creates an empty tier registry.
func NewRegistry() *Registry {
	return &Registry{
		byName: make(map[string]Tier),
		byRank: make(map[int64]Tier),
	}
}

// Add registers a tier.
func (r *Registry) Add(name string, rank, t, b int64) error {
	if name == "" || rank < 1 || rank > 1000 || t < 1 || t > 1_000_000 || b < 1 || b > 1_000_000 {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byName[name]; ok {
		return ErrDuplicate
	}
	if _, ok := r.byRank[rank]; ok {
		return ErrDuplicate
	}
	tier := Tier{Name: name, Rank: rank, T: t, B: b}
	r.byName[name] = tier
	r.byRank[rank] = tier
	if r.min == nil || rank < r.min.Rank {
		t := tier
		r.min = &t
	}
	return nil
}

// Resolve derives the effective tier from scopes.
func (r *Registry) Resolve(scopes []string) (Tier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.byName) == 0 {
		return Tier{}, ErrNoTier
	}
	var best *Tier
	for _, scope := range scopes {
		if len(scope) <= len(ScopePrefix) || scope[:len(ScopePrefix)] != ScopePrefix {
			continue
		}
		name := scope[len(ScopePrefix):]
		if tier, ok := r.byName[name]; ok {
			if best == nil || tier.Rank > best.Rank {
				copy := tier
				best = &copy
			}
		}
	}
	if best != nil {
		return *best, nil
	}
	return *r.min, nil
}
