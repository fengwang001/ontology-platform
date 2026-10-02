package dbscan

import "sort"

// collectChanges 收集一次局部重算涉及点的标签变化。
// 被删除点新标签为 -1；新插入点旧标签为 -1；其余点取变更前缓存标签。
func (s *Service) collectChanges(res *affectedResult, oldLabel map[int64]int64,
	inserted, deleted map[int64]bool) []Change {
	touched := make(map[int64]bool)
	for id := range deleted {
		touched[id] = true
	}
	for id := range inserted {
		touched[id] = true
	}
	for id := range res.boundary {
		touched[id] = true
	}
	for _, comp := range res.preComponents {
		for id := range comp {
			touched[id] = true
		}
	}
	for _, comp := range res.postComponents {
		for id := range comp {
			touched[id] = true
		}
	}
	var changes []Change
	for id := range touched {
		var oldL, newL int64
		switch {
		case deleted[id]:
			oldL = oldLabel[id]
			newL = -1
		case inserted[id]:
			oldL = -1
			newL = s.label[id]
		default:
			oldL = oldLabel[id]
			newL = s.label[id]
		}
		if oldL != newL {
			changes = append(changes, Change{ID: id, OldLabel: oldL, NewLabel: newL})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	return changes
}

// Insert 以当前时钟 now 为出生时刻加入点 (id,x,y)。
func (s *Service) Insert(id, x, y int64) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id <= 0 {
		return nil, invalidParam("id must be positive")
	}
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return nil, invalidParam("coordinates must be in [-1e6,1e6]")
	}
	if _, exists := s.points[id]; exists {
		return nil, &OpError{Reason: ReasonIDExists, Msg: "id already alive"}
	}
	if len(s.points) >= s.c {
		return nil, &OpError{Reason: ReasonCapacityFull, Msg: "alive point count already at C"}
	}

	p := &Point{ID: id, X: x, Y: y, Born: s.now}
	nb := s.addPointRaw(p)

	affected := map[int64]bool{id: true}
	for _, n := range nb {
		affected[n] = true
	}
	pre := s.preMutation(affected, nil, nil)
	res := s.recompute(affected, nil, pre)

	changes := s.collectChanges(res, res.oldLabels, map[int64]bool{id: true}, nil)
	return &Result{Changes: changes, Events: s.buildLocalEvents(res)}, nil
}

// Remove 手动删除指定存活点。
func (s *Service) Remove(id int64) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id <= 0 {
		return nil, invalidParam("id must be positive")
	}
	if _, exists := s.points[id]; !exists {
		return nil, &OpError{Reason: ReasonIDNotFound, Msg: "id not alive"}
	}

	affected := make(map[int64]bool)
	deletionSeeds := make(map[int64]bool)
	goneCores := make(map[int64]bool)
	if s.core[id] {
		goneCores[id] = true
	}
	oldSelfLabel := s.label[id]
	for nb := range s.adj[id] {
		if nb == id {
			continue
		}
		affected[nb] = true
		deletionSeeds[nb] = true
	}
	deleted := map[int64]bool{id: true}
	pre := s.preMutation(affected, deletionSeeds, goneCores)
	s.deletePointRaw(id)

	res := s.recompute(affected, deletionSeeds, pre)
	res.oldLabels[id] = oldSelfLabel

	changes := s.collectChanges(res, res.oldLabels, nil, deleted)
	return &Result{Changes: changes, Events: s.buildLocalEvents(res)}, nil
}

// Tick 把时钟推进到 t（只前进），并使所有满足 born+W<=now 的点过期。
func (s *Service) Tick(t int64) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t < 0 || t > maxTick {
		return nil, invalidParam("t must be in [0,1e15]")
	}
	if t < s.now {
		return nil, &OpError{Reason: ReasonClockRewind, Msg: "t must not be less than now"}
	}

	var expiring []int64
	for len(s.heap) > 0 {
		top := s.heap[0]
		if s.points[top].Born+s.w > t {
			break
		}
		expiring = append(expiring, top)
		s.heapRemove(top)
	}
	s.now = t
	if len(expiring) == 0 {
		return &Result{Changes: []Change{}}, nil
	}

	expireSet := make(map[int64]bool, len(expiring))
	affected := make(map[int64]bool)
	deletionSeeds := make(map[int64]bool)
	goneCores := make(map[int64]bool)
	oldSelfLabels := make(map[int64]int64)
	for _, id := range expiring {
		expireSet[id] = true
		oldSelfLabels[id] = s.label[id]
		if s.core[id] {
			goneCores[id] = true
		}
	}
	for _, id := range expiring {
		for nb := range s.adj[id] {
			if expireSet[nb] {
				continue
			}
			affected[nb] = true
			deletionSeeds[nb] = true
		}
	}
	pre := s.preMutation(affected, deletionSeeds, goneCores)
	for _, id := range expiring {
		s.deletePointRaw(id)
	}

	res := s.recompute(affected, deletionSeeds, pre)
	for id, lab := range oldSelfLabels {
		res.oldLabels[id] = lab
	}

	changes := s.collectChanges(res, res.oldLabels, nil, expireSet)
	return &Result{Changes: changes, Events: s.buildLocalEvents(res)}, nil
}

// Label 返回点当前标签；点不存在时 ok 为 false。
func (s *Service) Label(id int64) (lab int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lab, ok = s.label[id]
	return lab, ok
}

// Neighbors 返回 N(id) 的升序编号列表；点不存在时 ok 为 false。
func (s *Service) Neighbors(id int64) ([]int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.points[id]; !exists {
		return nil, false
	}
	nb := make([]int64, 0, len(s.adj[id]))
	for n := range s.adj[id] {
		nb = append(nb, n)
	}
	sort.Slice(nb, func(i, j int) bool { return nb[i] < nb[j] })
	return nb, true
}

// Clusters 返回按标签升序的 (标签, 成员升序) 列表，不含噪声。
func (s *Service) Clusters() []Cluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	byLabel := make(map[int64][]int64)
	for id, lab := range s.label {
		if lab == 0 {
			continue
		}
		byLabel[lab] = append(byLabel[lab], id)
	}
	labels := make([]int64, 0, len(byLabel))
	for lab := range byLabel {
		labels = append(labels, lab)
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i] < labels[j] })
	out := make([]Cluster, 0, len(labels))
	for _, lab := range labels {
		members := byLabel[lab]
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		out = append(out, Cluster{Label: lab, Members: members})
	}
	return out
}

// Alive 返回存活点数。
func (s *Service) Alive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.points)
}

// Now 返回当前系统时钟。
func (s *Service) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// RangeQueries 返回自构造以来发出的 eps 邻域查询总数。
func (s *Service) RangeQueries() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rangeQueries
}
