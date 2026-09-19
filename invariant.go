package ontology

import "sort"

// CheckInvariant verifies that the forward and reverse indexes are exact
// mirrors of each other. On the first discrepancy it returns an
// *InvariantError naming the link type, the side holding the orphan entry,
// and the endpoint pair. It is read-locked, so it can be called at any time,
// including from concurrent tests.
func (s *Store) CheckInvariant() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return checkInvariant(s.st)
}

func checkInvariant(st *state) error {
	for _, lt := range sortedLinkTypeNames(st) {
		if err := checkMirror(st, lt); err != nil {
			return err
		}
	}
	return nil
}

func checkMirror(st *state, lt string) error {
	for _, src := range sortedKeysOf(st.fwd[lt]) {
		for _, dst := range sortedKeys(st.fwd[lt][src]) {
			if !st.rev[lt][dst][src] {
				return &InvariantError{lt, "forward", src, dst,
					"entry missing from reverse index"}
			}
		}
	}
	for _, dst := range sortedKeysOf(st.rev[lt]) {
		for _, src := range sortedKeys(st.rev[lt][dst]) {
			if !st.fwd[lt][src][dst] {
				return &InvariantError{lt, "reverse", src, dst,
					"entry missing from forward index"}
			}
		}
	}
	return nil
}

// sortedKeysOf is a small helper for deterministic iteration in checks.
func sortedKeysOf(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
