package cache

import "sort"

// naiveCache 是按题目规则写成的线性表朴素实现，用于与 Cache 对拍。
// 所有判定都通过全表线性扫描完成，逻辑直白、便于人工核对。
type naiveCache struct {
	m    int
	capN int
	tick uint64
	all  []*naiveEntry
}

type naiveEntry struct {
	key      string
	manifest []Pair
	result   string
	last     uint64
}

func newNaive(m, capN int) *naiveCache {
	return &naiveCache{m: m, capN: capN}
}

func manifestEqual(a, b []Pair) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (n *naiveCache) put(key string, manifest []Pair, result string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if result == "" {
		return ErrEmptyResult
	}
	for _, e := range n.all {
		if e.key == key && manifestEqual(e.manifest, manifest) {
			e.result = result
			n.tick++
			e.last = n.tick
			return nil
		}
	}
	n.tick++
	n.all = append(n.all, &naiveEntry{
		key:      key,
		manifest: append([]Pair(nil), manifest...),
		result:   result,
		last:     n.tick,
	})
	// 键内淘汰先于全局淘汰。
	for n.keyCount(key) > n.m {
		n.evict(n.minLast(func(e *naiveEntry) bool { return e.key == key }))
	}
	for len(n.all) > n.capN {
		n.evict(n.minLast(func(e *naiveEntry) bool { return true }))
	}
	return nil
}

func (n *naiveCache) lookup(key string, current map[string]string) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	var best *naiveEntry
	for _, e := range n.all {
		if e.key != key || !manifestHit(e.manifest, current) {
			continue
		}
		if best == nil || e.last > best.last {
			best = e
		}
	}
	if best == nil {
		return "", false, nil
	}
	n.tick++
	best.last = n.tick
	return best.result, true, nil
}

func (n *naiveCache) dump(key string) []Entry {
	var out []Entry
	for _, e := range n.all {
		if e.key == key {
			out = append(out, Entry{
				Manifest: append([]Pair(nil), e.manifest...),
				Result:   e.result,
				Last:     e.last,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last > out[j].Last })
	return out
}

func (n *naiveCache) keyCount(key string) int {
	count := 0
	for _, e := range n.all {
		if e.key == key {
			count++
		}
	}
	return count
}

func (n *naiveCache) minLast(match func(*naiveEntry) bool) *naiveEntry {
	var victim *naiveEntry
	for _, e := range n.all {
		if match(e) && (victim == nil || e.last < victim.last) {
			victim = e
		}
	}
	return victim
}

func (n *naiveCache) evict(victim *naiveEntry) {
	for i, e := range n.all {
		if e == victim {
			n.all = append(n.all[:i], n.all[i+1:]...)
			return
		}
	}
}
