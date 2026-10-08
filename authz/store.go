package authz

import "sync"

// snapshot is an immutable, fully-indexed view of one policy-set version.
type snapshot struct {
	version uint64
	// byNamespace indexes every policy under its own namespace; root
	// namespace policies additionally live under the root key only, so
	// evaluation touches exactly two buckets (target + root).
	byNamespace map[string][]*Policy
}

// Store holds the atomically replaceable policy set. All methods are safe
// for concurrent use; any interleaving of calls is equivalent to some
// serial order, and an evaluation always observes one complete version.
type Store struct {
	rootNamespace string

	mu      sync.RWMutex
	version uint64
	snap    *snapshot
}

// NewStore creates a store with the given mesh root namespace. Policies in
// the root namespace apply to every namespace in the mesh.
func NewStore(rootNamespace string) (*Store, error) {
	if rootNamespace == "" {
		return nil, argErr("rootNamespace", "must not be empty")
	}
	s := &Store{rootNamespace: rootNamespace}
	s.snap = &snapshot{version: 0, byNamespace: map[string][]*Policy{}}
	return s, nil
}

// RootNamespace returns the configured mesh root namespace.
func (s *Store) RootNamespace() string { return s.rootNamespace }

// Version returns the current policy-set version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// ReplaceAll validates the submitted set and, only if it is fully legal,
// atomically swaps it in and bumps the version by one. Any problem aborts
// the whole replacement: the version and the visible set stay unchanged,
// and the first problem (in submission order) is reported as an
// ErrKindInvalidPolicySet error.
func (s *Store) ReplaceAll(policies []Policy) (uint64, error) {
	if err := validatePolicySet(policies); err != nil {
		return 0, err
	}
	byNS := make(map[string][]*Policy)
	for i := range policies {
		p := policies[i]
		byNS[p.Namespace] = append(byNS[p.Namespace], &p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	s.snap = &snapshot{version: s.version, byNamespace: byNS}
	return s.version, nil
}

// Evaluate decides the request against one complete snapshot of the policy
// set. A malformed request yields ErrKindInvalidArgument; evaluation never
// fails because of policy content.
func (s *Store) Evaluate(req Request) (Result, error) {
	res, _, err := s.evaluate(req)
	return res, err
}

// evaluate is Evaluate plus the number of candidate policies examined, so
// tests can prove the cost does not grow with unrelated namespaces.
func (s *Store) evaluate(req Request) (Result, int, error) {
	if err := validateRequest(&req); err != nil {
		return Result{}, 0, err
	}
	s.mu.RLock()
	snap := s.snap
	s.mu.RUnlock()
	res, examined := evaluate(snap, s.rootNamespace, &req)
	return res, examined, nil
}
