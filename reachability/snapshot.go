package reachability

import "sort"

// Snapshot 是某一时刻可达关系的不可变快照。
// Snapshot 发布后其内部数据不再变更，并发读取无需加锁，
// 读到的全部点对来自同一个一致版本（逐点对一致）。
type Snapshot struct {
	// reach[from] 是 from 经长度至少为 1 的有向路径可达的目标点集合。
	reach map[string]map[string]struct{}
}

// Reachable 判断在该快照版本下 from 是否可达 to（路径长度至少为 1）。
// 不存在于图中的节点名不构成错误，只返回 false。
func (s *Snapshot) Reachable(from, to string) bool {
	if s == nil {
		return false
	}
	dests, ok := s.reach[from]
	if !ok {
		return false
	}
	_, ok = dests[to]
	return ok
}

// Pairs 按字典序返回该快照中的全部可达点对（先按 From 再按 To）。
// 返回的切片归调用方所有，与后续图变更互不影响。
func (s *Snapshot) Pairs() []Pair {
	if s == nil || len(s.reach) == 0 {
		return []Pair{}
	}
	froms := make([]string, 0, len(s.reach))
	for f := range s.reach {
		if len(s.reach[f]) > 0 {
			froms = append(froms, f)
		}
	}
	sort.Strings(froms)

	var pairs []Pair
	for _, f := range froms {
		tos := s.sortedDestinations(f)
		for _, t := range tos {
			pairs = append(pairs, Pair{From: f, To: t})
		}
	}
	return pairs
}

// ReachableFrom 按字典序返回从 from 出发可达的全部目标点。
func (s *Snapshot) ReachableFrom(from string) []string {
	if s == nil {
		return []string{}
	}
	return s.sortedDestinations(from)
}

func (s *Snapshot) sortedDestinations(from string) []string {
	dests := s.reach[from]
	if len(dests) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(dests))
	for t := range dests {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Sources 按字典序返回所有可达 to 的源点（便于测试与排查）。
func (s *Snapshot) Sources(to string) []string {
	if s == nil {
		return []string{}
	}
	var out []string
	for f, dests := range s.reach {
		if _, ok := dests[to]; ok {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// Size 返回可达点对总数。
func (s *Snapshot) Size() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, dests := range s.reach {
		n += len(dests)
	}
	return n
}

// clone 复制一份可继续写入的内部映射（构建新快照时使用）。
func (s *Snapshot) clone() map[string]map[string]struct{} {
	next := make(map[string]map[string]struct{}, len(s.reach))
	for f, dests := range s.reach {
		cp := make(map[string]struct{}, len(dests))
		for t := range dests {
			cp[t] = struct{}{}
		}
		next[f] = cp
	}
	return next
}

// newSnapshot 以给定内部映射构造快照（映射所有权转移给快照，之后不得再改）。
func newSnapshot(reach map[string]map[string]struct{}) *Snapshot {
	if reach == nil {
		reach = map[string]map[string]struct{}{}
	}
	return &Snapshot{reach: reach}
}

// emptySnapshot 返回空快照。
func emptySnapshot() *Snapshot {
	return newSnapshot(map[string]map[string]struct{}{})
}
