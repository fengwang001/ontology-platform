package ontology

import "sort"

// LinkView 回放结果中的一条链接。刻意不包含任何记录来源信息：
// 无论事实最初由哪一端触发记录，回放输出都呈现为同一份不可分割的历史。
type LinkView struct {
	Left  ObjectID `json:"left"`
	Right ObjectID `json:"right"`
	// SymmetryDeficit 为 true 表示该对称链接存在结构性镜像缺损
	// （单端记录且对端始终未补录确认）。该字段只声明缺损存在，
	// 不暴露缺损来自哪一端的记录。
	SymmetryDeficit bool `json:"symmetryDeficit,omitempty"`
}

// ReplayResult 某一记录时刻回放得到的链接集合。
type ReplayResult struct {
	RecordTime int64      `json:"recordTime"`
	ValidAt    int64      `json:"validAt"`
	Links      []LinkView `json:"links"`
}

// contains 判断回放结果中是否存在链接 (a, b)（对称链接任一方向均可命中）。
func (r ReplayResult) contains(def LinkTypeDef, a, b ObjectID) bool {
	p := canonicalPair(def, a, b)
	for _, l := range r.Links {
		if l.Left == p.L && l.Right == p.R {
			return true
		}
	}
	return false
}

// replayState 在记录时刻 rt 重建内部链接状态表。
// 代价上界：O(log 快照数) 定位 + 克隆当时存活状态 + 应用至多 stride 条增量，
// 与累计事实总量无关。steps 计数器记录关键操作次数供独立验证。
func (s *Store) replayState(st *linkTypeState, rt int64) map[pair]*pairState {
	var steps int64
	// 二分定位最后一个 at <= rt 的快照。
	idx := sort.Search(len(st.snaps), func(i int) bool {
		steps++
		return st.snaps[i].at > rt
	}) - 1

	var states map[pair]*pairState
	from := 0
	if idx >= 0 {
		snap := st.snaps[idx]
		states = cloneStates(snap.states)
		steps += int64(len(snap.states))
		from = snap.count
	} else {
		states = make(map[pair]*pairState)
	}
	// 应用快照之后的增量事实（至多 snapshotStride 条，末尾可能不足一个步长）。
	for i := from; i < len(st.facts) && st.facts[i].RecordedAt <= rt; i++ {
		applyFact(states, st.facts[i], st.def)
		steps++
	}
	s.lastReplaySteps = steps
	return states
}

// present 从状态表中筛出在有效时刻 vt 实际存在的链接。
func present(states map[pair]*pairState, vt int64) []LinkView {
	var out []LinkView
	for p, ps := range states {
		for _, iv := range ps.ivs {
			if iv.covers(vt) {
				out = append(out, LinkView{Left: p.L, Right: p.R, SymmetryDeficit: ps.deficit})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Left != out[j].Left {
			return out[i].Left < out[j].Left
		}
		return out[i].Right < out[j].Right
	})
	return out
}

// Replay 回放在记录时刻 rt、有效时刻 vt 实际存在的链接集合。
// 正向与反向视角由同一份规范化状态派生，严格互为镜像。
func (s *Store) Replay(lt LinkTypeID, rt, vt int64) ReplayResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.links[lt]
	if st == nil {
		return ReplayResult{RecordTime: rt, ValidAt: vt}
	}
	return ReplayResult{
		RecordTime: rt,
		ValidAt:    vt,
		Links:      present(s.replayState(st, rt), vt),
	}
}

// ReplayFrom 从对象 obj 所在的一端出发，回放其在 (rt, vt) 的对端集合。
// 与 Replay 使用同一状态表，因此两端视角必然一致。
func (s *Store) ReplayFrom(lt LinkTypeID, obj ObjectID, rt, vt int64) []ObjectID {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.links[lt]
	if st == nil {
		return nil
	}
	states := s.replayState(st, rt)
	var out []ObjectID
	for p, ps := range states {
		alive := false
		for _, iv := range ps.ivs {
			if iv.covers(vt) {
				alive = true
				break
			}
		}
		if !alive {
			continue
		}
		switch obj {
		case p.L:
			out = append(out, p.R)
		case p.R:
			out = append(out, p.L)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
