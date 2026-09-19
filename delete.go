package ontology

// DeleteObject 删除对象并沿链递归结算级联语义。
// 级联链路上任意一处触发 RESTRICT 时整次删除不生效，状态完全回滚。
func (s *Store) DeleteObject(key ObjectKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(key)
}

func (s *Store) deleteLocked(key ObjectKey) error {
	if _, ok := s.objects[key]; !ok {
		return &ObjectNotFoundError{Source: key, Target: key, Side: "object"}
	}
	plan, err := s.planDeleteLocked(key)
	if err != nil {
		return err
	}
	for _, l := range plan.links {
		s.removeLinkLocked(l.LinkType, l.Source, l.Target)
	}
	for _, obj := range plan.objects {
		delete(s.objects, obj)
	}
	return nil
}

// deletePlan 是一次删除的完整结算计划：先规划后执行，规划失败则零副作用。
type deletePlan struct {
	objects []ObjectKey
	links   []Link
}

// planDeleteLocked 以 BFS 沿链结算：CASCADE 入队对端，SET_NULL 仅断链，
// RESTRICT 在对端不会被同次级联删除时拒绝整次删除。
// 通过 inPlan/queued 去重，环上每个对象只结算一次，保证终止。
func (s *Store) planDeleteLocked(root ObjectKey) (*deletePlan, error) {
	plan := &deletePlan{}
	inPlan := map[ObjectKey]bool{}
	queued := map[ObjectKey]bool{root: true}
	prev := map[ObjectKey]ObjectKey{}
	prevStep := map[ObjectKey]PathStep{}
	linkSeen := map[Link]bool{}
	queue := []ObjectKey{root}
	addLink := func(ltName string, src, tgt ObjectKey) {
		l := Link{LinkType: ltName, Source: src, Target: tgt}
		if !linkSeen[l] {
			linkSeen[l] = true
			plan.links = append(plan.links, l)
		}
	}
	// visit 处理一条触及 obj 的链，peer 为对端。
	visit := func(obj, peer ObjectKey, lt LinkType, src, tgt ObjectKey) error {
		addLink(lt.Name, src, tgt)
		if inPlan[peer] || queued[peer] {
			return nil // 对端已在本次级联中，链随对象一起消失
		}
		switch lt.Cascade {
		case CascadeDelete:
			queued[peer] = true
			prev[peer] = obj
			prevStep[peer] = PathStep{LinkType: lt.Name, Source: src, Target: tgt}
			queue = append(queue, peer)
		case CascadeSetNull:
			// 仅断链
		case CascadeRestrict:
			return &RestrictError{
				LinkType: lt.Name,
				Source:   src,
				Target:   tgt,
				Path:     buildPath(root, obj, prev, prevStep, PathStep{LinkType: lt.Name, Source: src, Target: tgt}),
			}
		}
		return nil
	}
	for len(queue) > 0 {
		obj := queue[0]
		queue = queue[1:]
		if inPlan[obj] {
			continue
		}
		inPlan[obj] = true
		plan.objects = append(plan.objects, obj)
		for _, ltName := range s.sortedLinkTypeNames() {
			lt := s.linkTypes[ltName]
			for _, tgt := range sortedKeys(s.fwd[ltName][obj]) {
				if err := visit(obj, tgt, lt, obj, tgt); err != nil {
					return nil, err
				}
			}
			for _, src := range sortedKeys(s.bwd[ltName][obj]) {
				if err := visit(obj, src, lt, src, obj); err != nil {
					return nil, err
				}
			}
		}
	}
	return plan, nil
}

// buildPath 还原从 root 到触发 RESTRICT 的链的完整路径。
func buildPath(root, obj ObjectKey, prev map[ObjectKey]ObjectKey, prevStep map[ObjectKey]PathStep, last PathStep) []PathStep {
	var rev []PathStep
	for cur := obj; cur != root; cur = prev[cur] {
		rev = append(rev, prevStep[cur])
	}
	path := make([]PathStep, 0, len(rev)+1)
	for i := len(rev) - 1; i >= 0; i-- {
		path = append(path, rev[i])
	}
	return append(path, last)
}
