package layerconfig

import "reflect"

// This file exposes whitebox inspection helpers used by the performance
// tests to verify structural sharing. They are not part of the configuration
// API and must not be relied on by production callers.

// SharedWriteShards returns, for the newest and second-newest retained
// versions, the number of write-pmap shards whose backing map is shared by
// pointer identity. At version < 2 it returns 0.
//
// The returned value is "additional shared shards compared with a baseline":
// call before and after a one-key publish; the delta is the shared count.
func SharedWriteShards(s *Store) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur := s.head.Load().version
	if cur < 1 {
		return 0
	}
	a := s.head.Load().history[cur-1]
	b := s.head.Load().history[cur]
	return sharedShardCount(a, b)
}

func sharedShardCount(a, b *state) int {
	if a == nil || b == nil {
		return 0
	}
	n := 0
	for i := 0; i < pmapShardCount; i++ {
		sa := a.writes.shards[i]
		sb := b.writes.shards[i]
		if sa == nil || sb == nil {
			// Empty shards are represented by nil on both sides.
			if sa == nil && sb == nil {
				n++
			}
			continue
		}
		if reflect.ValueOf(sa).Pointer() == reflect.ValueOf(sb).Pointer() {
			n++
		}
	}
	return n
}

// TotalWriteShardMaps counts distinct backing maps across the write pmaps of
// every retained version. A full-copy implementation would allocate
// versions*64 maps; structural sharing keeps this much smaller.
func TotalWriteShardMaps(s *Store) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[uintptr]bool{0: true}
	for _, st := range s.head.Load().history {
		for i := 0; i < pmapShardCount; i++ {
			sh := st.writes.shards[i]
			if sh == nil {
				continue
			}
			seen[reflect.ValueOf(sh).Pointer()] = true
		}
	}
	return len(seen) - 1 // exclude nil
}
