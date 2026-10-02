package percolator

func (v Version) hasRollbackOf(st int64) bool {
	return v.Kind == Rollback && v.StartTS == st && v.CommitTS == st
}

func locksEqual(a, b *Lock) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func snapshotsEqual(a, b Snapshot) bool {
	if a.Oracle != b.Oracle || a.Water != b.Water || len(a.Keys) != len(b.Keys) {
		return false
	}
	for key, ka := range a.Keys {
		kb, ok := b.Keys[key]
		if !ok {
			return false
		}
		if (ka.Lock == nil) != (kb.Lock == nil) {
			return false
		}
		if ka.Lock != nil && *ka.Lock != *kb.Lock {
			return false
		}
		if len(ka.Versions) != len(kb.Versions) {
			return false
		}
		for i := range ka.Versions {
			if ka.Versions[i] != kb.Versions[i] {
				return false
			}
		}
	}
	return true
}
