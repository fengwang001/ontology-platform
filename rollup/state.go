package rollup

import (
	"sort"

	"ontology/tier"
)

// L0Points 返回 L0 原始点的有序副本，供检查与测试。
func (s *Store) L0Points() []tier.Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tier.Point(nil), s.st.l0...)
}

// L1Bucket 返回分钟桶 m 的内容及是否存在。
func (s *Store) L1Bucket(m int64) (tier.Bucket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.st.l1[m]
	return b, ok
}

// L2Bucket 返回小时桶 h 的内容及是否存在。
func (s *Store) L2Bucket(h int64) (tier.Bucket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.st.l2[h]
	return b, ok
}

// state 是存储的三层内容。L0 点按 (TS,V) 升序保存，保证重放与输出确定。
type state struct {
	l0 []tier.Point
	l1 map[int64]tier.Bucket // key: 分钟 m
	l2 map[int64]tier.Bucket // key: 小时 h
}

func newState() state {
	return state{
		l1: make(map[int64]tier.Bucket),
		l2: make(map[int64]tier.Bucket),
	}
}

// clone 返回逐字段深拷贝，供 Advance 在副本上试执行。
func (s state) clone() state {
	cp := state{
		l0: append([]tier.Point(nil), s.l0...),
		l1: make(map[int64]tier.Bucket, len(s.l1)),
		l2: make(map[int64]tier.Bucket, len(s.l2)),
	}
	for k, v := range s.l1 {
		cp.l1[k] = v
	}
	for k, v := range s.l2 {
		cp.l2[k] = v
	}
	return cp
}

// units 返回占用的总单位数：每个 L0 点、每个 L1 桶、每个 L2 桶各 1。
func (s state) units() int64 {
	return int64(len(s.l0) + len(s.l1) + len(s.l2))
}

// insertPoint 把点插入 L0 并保持 (TS,V) 有序。
func (s *state) insertPoint(p tier.Point) {
	idx := sort.Search(len(s.l0), func(i int) bool {
		return s.l0[i].TS > p.TS || (s.l0[i].TS == p.TS && s.l0[i].V >= p.V)
	})
	s.l0 = append(s.l0, tier.Point{})
	copy(s.l0[idx+1:], s.l0[idx:])
	s.l0[idx] = p
}

// sortedKeys 返回 map 的升序 key。
func sortedKeys(m map[int64]tier.Bucket) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// deleteHour 从三层删除小时 h 的全部数据（L0 按点归属、L1 按分钟归属、L2 整桶）。
func (s *state) deleteHour(h int64) {
	hf, ht := tier.HourRange(h)
	kept := s.l0[:0]
	for _, p := range s.l0 {
		if p.TS < hf || p.TS >= ht {
			kept = append(kept, p)
		}
	}
	s.l0 = kept
	for m := range s.l1 {
		mf, _ := tier.MinuteRange(m)
		if mf >= hf && mf < ht {
			delete(s.l1, m)
		}
	}
	delete(s.l2, h)
}
