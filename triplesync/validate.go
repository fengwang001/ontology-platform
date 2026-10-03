package triplesync

// validateSnapshot checks id/name/hash rules, sibling uniqueness, parent
// existence and acyclicity.
func validateSnapshot(s Snapshot) bool {
	type sibKey struct {
		parent int64
		name   string
	}
	seen := make(map[sibKey]struct{}, len(s))
	for id, e := range s {
		if id < 1 {
			return false
		}
		if e.Name == "" {
			return false
		}
		for i := 0; i < len(e.Name); i++ {
			if e.Name[i] == '/' {
				return false
			}
		}
		if e.Dir {
			if e.Hash != "" {
				return false
			}
		} else if e.Hash == "" {
			return false
		}
		if e.Parent != 0 {
			pe, ok := s[e.Parent]
			if !ok || !pe.Dir {
				return false
			}
		}
		key := sibKey{e.Parent, e.Name}
		if _, dup := seen[key]; dup {
			return false
		}
		seen[key] = struct{}{}
	}
	// Acyclicity: walk every parent chain; visiting an in-progress node is a
	// cycle, a finished node is a valid memoized depth.
	const (
		stateProgress = -1
	)
	depth := make(map[int64]int, len(s))
	depth[0] = 0
	var resolve func(id int64) (int, bool)
	resolve = func(id int64) (int, bool) {
		if d, ok := depth[id]; ok {
			if d == stateProgress {
				return 0, false
			}
			return d, true
		}
		depth[id] = stateProgress
		pd, ok := resolve(s[id].Parent)
		if !ok {
			return 0, false
		}
		depth[id] = pd + 1
		return pd + 1, true
	}
	for id := range s {
		if depth[id] == stateProgress {
			return false
		}
		if _, ok := resolve(id); !ok {
			return false
		}
	}
	return true
}

// depths returns the depth (number of ancestors) of every entry. Callers must
// pass a snapshot known to be acyclic.
func depths(s Snapshot) map[int64]int {
	d := make(map[int64]int, len(s))
	d[0] = 0
	var calc func(id int64) int
	calc = func(id int64) int {
		if v, ok := d[id]; ok {
			return v
		}
		v := calc(s[id].Parent) + 1
		d[id] = v
		return v
	}
	for id := range s {
		calc(id)
	}
	return d
}

// dirAgreement verifies the Dir flag of every shared id against the base.
// Ids absent from the base must agree between the two sides; when they
// disagree the remote side is declared illegal.
func dirAgreement(base, local, remote Snapshot) error {
	for id, le := range local {
		if be, ok := base[id]; ok {
			if le.Dir != be.Dir {
				return ErrBadLocal
			}
		} else if re, ok := remote[id]; ok {
			if le.Dir != re.Dir {
				return ErrBadRemote
			}
		}
	}
	for id, re := range remote {
		if be, ok := base[id]; ok {
			if re.Dir != be.Dir {
				return ErrBadRemote
			}
		}
	}
	return nil
}
