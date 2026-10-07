package delegation

import "time"

// Delegate 声明一条新委托。
//
// 校验按固定优先顺序进行，任一失败都不会改变任何已有状态：
//  1. 声明子集必须 ⊆ 委托方当前有效权限，否则 ErrSubsetExceeds；
//  2. 声明子集必须 ⊆ 委托方当前可再委托权限（原始权限加上游允许
//     再委托的部分），否则 ErrRedelegateNotAllowed；
//  3. 新增委托不得使委托链成环，否则 ErrCycle；
//  4. 有效期必须合法且尚未结束，否则 ErrExpired。
func (s *Service) Delegate(req DelegationRequest) (DelegationID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()

	entry := LogEntry{Time: now, Op: "Delegate", Input: req}
	defer func() { s.log.Log(entry) }()

	ctx := newEvalContext(now)

	// 1. 子集不得超出委托方当前实际拥有的有效权限。
	if !req.Subset.IsSubsetOf(s.effective(req.Delegator, ctx)) {
		entry.Error = ErrSubsetExceeds.Error()
		return 0, ErrSubsetExceeds
	}
	// 2. 子集不得超出委托方可再委托的范围。
	if !req.Subset.IsSubsetOf(s.redelegatable(req.Delegator, ctx)) {
		entry.Error = ErrRedelegateNotAllowed.Error()
		return 0, ErrRedelegateNotAllowed
	}
	// 3. 成环检测：若受托方沿现有委托边已能到达委托方，
	//    则新边 委托方->受托方 将闭合一个环。
	if s.reachable(req.Delegatee, req.Delegator, now) {
		entry.Error = ErrCycle.Error()
		return 0, ErrCycle
	}
	// 4. 有效期必须非空且尚未结束。
	if !req.End.After(req.Start) || !req.End.After(now) {
		entry.Error = ErrExpired.Error()
		return 0, ErrExpired
	}

	d := &Delegation{
		ID:              s.nextID,
		Delegator:       req.Delegator,
		Delegatee:       req.Delegatee,
		Subset:          req.Subset.Clone(),
		Start:           req.Start,
		End:             req.End,
		AllowRedelegate: req.AllowRedelegate,
		CreatedAt:       now,
	}
	s.nextID++
	s.delegations[d.ID] = d
	s.byDelegatee[d.Delegatee] = append(s.byDelegatee[d.Delegatee], d)
	s.byDelegator[d.Delegator] = append(s.byDelegator[d.Delegator], d)

	entry.Output = map[string]any{"id": d.ID}
	return d.ID, nil
}

// Revoke 撤销一条委托记录。撤销以追加 RevokedAt 的方式记录，
// 不修改历史，因此撤销时刻之前的历史判定结论保持不变。
func (s *Service) Revoke(id DelegationID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()

	entry := LogEntry{Time: now, Op: "Revoke", Input: map[string]any{"id": id}}
	defer func() { s.log.Log(entry) }()

	d, ok := s.delegations[id]
	if !ok {
		entry.Error = ErrDelegationNotFound.Error()
		return ErrDelegationNotFound
	}
	if d.RevokedAt != nil {
		entry.Error = ErrDelegationRevokedDone.Error()
		return ErrDelegationRevokedDone
	}
	d.RevokedAt = &now
	return nil
}

// reachable 报告从 from 出发，沿"委托方 -> 受托方"方向的现存委托边
// 是否能到达 to。只考虑当前或未来仍可能生效的边
// （未撤销且 End 晚于 now）。调用方必须持有锁。
func (s *Service) reachable(from, to string, now time.Time) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, d := range s.byDelegator[cur] {
			if d.RevokedAt != nil || !d.End.After(now) {
				continue
			}
			if d.Delegatee == to {
				return true
			}
			if !seen[d.Delegatee] {
				seen[d.Delegatee] = true
				stack = append(stack, d.Delegatee)
			}
		}
	}
	return false
}

// GetDelegation 返回委托记录的快照（深拷贝），供审计与测试使用。
func (s *Service) GetDelegation(id DelegationID) (Delegation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.delegations[id]
	if !ok {
		return Delegation{}, false
	}
	cp := *d
	cp.Subset = d.Subset.Clone()
	return cp, true
}
