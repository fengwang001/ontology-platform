package layerconfig

// pmapShardCount is the fixed number of buckets. It is a compile-time
// constant, so a root update always copies the same-sized bucket pointer
// array independently of how many keys exist.
const pmapShardCount = 64

// pmap is a persistent (immutable, structurally shared) map from string to
// entry. Update operations return a new map; the receiver is never mutated.
//
// Implementation: a fixed-size array of immutable shard maps. get touches one
// shard (O(1)); set/delete copy only the affected shard (O(1/64) of the data)
// and share all other shards with the previous version. This gives:
//
//   - read cost independent of the number of keys, layers and versions;
//   - per-publication storage proportional to the changed shards only, never
//     to the size of the whole configuration.
type pmap struct {
	shards [pmapShardCount]map[string]entry
}

func emptyPmap() *pmap { return &pmap{} }

func shardIndex(key string) uint32 {
	// FNV-1a 32-bit; avoids importing hash/fnv in hot signatures.
	const offset32 = 2166136261
	const prime32 = 16777619
	h := uint32(offset32)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= prime32
	}
	return h % pmapShardCount
}

func (m *pmap) get(key string) (entry, bool) {
	idx := shardIndex(key)
	sh := m.shards[idx]
	if sh == nil {
		return entry{}, false
	}
	e, ok := sh[key]
	return e, ok
}

// withShard returns a new pmap whose shard idx is replaced by next. Every
// other shard is shared by reference.
func (m *pmap) withShard(idx uint32, next map[string]entry) *pmap {
	out := &pmap{}
	out.shards = m.shards // copies the fixed-size pointer-header array, O(64)
	out.shards[idx] = next
	return out
}

func (m *pmap) set(key string, e entry) *pmap {
	idx := shardIndex(key)
	old := m.shards[idx]
	next := make(map[string]entry, len(old)+1)
	for k, v := range old {
		next[k] = v
	}
	next[key] = e
	return m.withShard(idx, next)
}

func (m *pmap) delete(key string) *pmap {
	idx := shardIndex(key)
	old := m.shards[idx]
	if _, ok := old[key]; !ok {
		return m
	}
	next := make(map[string]entry, len(old))
	for k, v := range old {
		if k != key {
			next[k] = v
		}
	}
	if len(next) == 0 {
		next = nil
	}
	return m.withShard(idx, next)
}

// len iterates every shard; used only in tests and diagnostics, never on the
// read path.
func (m *pmap) len() int {
	n := 0
	for _, sh := range m.shards {
		n += len(sh)
	}
	return n
}

// forEach invokes fn for every key/entry pair. Used by the required-field
// validation, which iterates only the existence index (one small map), not
// the whole write store.
func (m *pmap) forEach(fn func(key string, e entry) bool) {
	for _, sh := range m.shards {
		for k, v := range sh {
			if !fn(k, v) {
				return
			}
		}
	}
}
