package ontology

// readForAttempt 是每次尝试提交前必须执行的“重新读取”：
// 它在目标实例的同一把锁、同一时点上取到当前基线版本、
// “本次请求涉及的每个目标侧约束”的当前计数，以及全量链接快照。
// 基数判定只使用 K 个计数器读（K=本次涉及的约束数，含取值为 0 的缺失键），
// 与链接总数无关；全量链接快照仅写入审计记录供重放核验。
// 前一次尝试读到的任何关联集合或计数都不会传入本函数，从结构上杜绝复用。
func (s *Store) readForAttempt(id string, ops []Op) (version uint64, counts map[ConstraintKey]int, snapshot []Link, err error) {
	mu := s.lockFor(id)
	if mu == nil {
		return 0, nil, nil, &ErrNotFound{What: "instance " + id}
	}
	mu.Lock()
	defer mu.Unlock()
	st := s.stateFor(id)
	keySet := map[ConstraintKey]bool{}
	for _, op := range ops {
		if lt, ok := s.linkType(op.TypeID); ok && lt.Constraint(op.Side) != nil {
			keySet[ConstraintKey{TypeID: op.TypeID, Side: op.Side}] = true
		}
	}
	s.bumpCounterReads(len(keySet))
	counts = make(map[ConstraintKey]int, len(keySet))
	for k := range keySet {
		counts[k] = st.counts[k] // 缺失键读取值 0，仍是一次 O(1) 计数读
	}
	return st.version, counts, s.snapshotLocked(st), nil
}

// keyVerdict 在锁外依据本次重新读取到的计数，给出单个目标侧约束的审计判定。
// 该计算不遍历链接集合：delta 由本次请求的操作与本次快照成员资格 O(1) 得出。
func keyVerdict(s *Store, key ConstraintKey, current int, ops []Op, target string, snapshot map[string]bool) ConstraintVerdict {
	// 先按链接合并净效果（与 Store.planOps 语义一致），避免重复操作被重复计数。
	intent := map[string]bool{}
	for _, op := range ops {
		if op.TypeID != key.TypeID || op.Side != key.Side {
			continue
		}
		lk := Link{TypeID: op.TypeID}
		if op.Side == SideA {
			lk.A, lk.B = target, op.Other
		} else {
			lk.A, lk.B = op.Other, target
		}
		intent[lk.Key()] = op.Add
	}
	delta := 0
	for keyStr, add := range intent {
		switch {
		case add && !snapshot[keyStr]:
			delta++
		case !add && snapshot[keyStr]:
			delta--
		}
	}
	lt, _ := s.linkType(key.TypeID)
	max := 0
	if c := lt.Constraint(key.Side); c != nil {
		max = c.Max
	}
	return ConstraintVerdict{
		Constraint: key,
		Current:    current,
		Delta:      delta,
		Max:        max,
		Satisfied:  max <= 0 || current+delta <= max,
	}
}
