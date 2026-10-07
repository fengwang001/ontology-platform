package delegation

import "time"

// Decide 对一次访问请求进行判定。
//
// req.At 为零值时按当前时刻判定；否则按指定历史时刻判定。
// 判定总是依据 req.At 时刻各委托链的有效期与各环节权限状态重新计算，
// 不缓存任何历史判定结果；由于全部状态变更仅以追加方式记录，
// 对同一历史时刻的重复判定永远返回相同结论。
//
// 返回的 Decision.NodesVisited 是本次判定实际检查过的委托记录条数，
// 该数字只随支持本次访问的委托路径规模增长，与系统中累计的
// 委托记录总数无关（见遍历只沿 byDelegatee 索引的反向边进行）。
func (s *Service) Decide(req AccessRequest) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()

	at := req.At
	now := s.clock.Now()
	if at.IsZero() {
		at = now
	}

	entry := LogEntry{Time: now, Op: "Decide", Input: req}
	defer func() { s.log.Log(entry) }()

	ctx := newEvalContext(at)
	eff := s.effective(req.Subject, ctx)
	allowed := PermissionSet{req.ObjectType: req.Require}.IsSubsetOf(eff)

	dec := Decision{Allowed: allowed}
	if allowed {
		need := PermissionSet{req.ObjectType: req.Require.Clone()}
		dec.Witness = s.witnessChain(req.Subject, need, ctx, make(map[string]bool))
	}
	dec.NodesVisited = ctx.visited

	entry.Output = dec
	entry.Witness = dec.Witness
	return dec
}

// EffectivePermissions 返回主体在时刻 t 的有效权限快照（深拷贝），
// 供审计、调试与测试使用。
func (s *Service) EffectivePermissions(principal string, at time.Time) PermissionSet {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.IsZero() {
		at = s.clock.Now()
	}
	return s.effective(principal, newEvalContext(at)).Clone()
}

// witnessChain 为"主体 p 在 ctx.t 时刻有效拥有 need"构造委托链依据：
// 返回一组支撑该结论的委托记录 ID（可能包含多条链）。
// 仅在结论成立时调用。调用方必须持有锁。
func (s *Service) witnessChain(p string, need PermissionSet, ctx *evalContext, visiting map[string]bool) []DelegationID {
	remaining := need.Subtract(s.baseAt(p, ctx.t))
	if len(remaining) == 0 {
		return nil
	}
	if visiting[p] {
		return nil
	}
	visiting[p] = true
	defer delete(visiting, p)

	var out []DelegationID
	for _, d := range s.byDelegatee[p] {
		if len(remaining) == 0 {
			break
		}
		ctx.visited++
		if !activeAt(d, ctx.t) {
			continue
		}
		contrib := remaining.Intersect(d.Subset)
		if len(contrib) == 0 {
			continue
		}
		// 该委托必须被其委托方完整支撑才生效。
		if !d.Subset.IsSubsetOf(s.effective(d.Delegator, ctx)) {
			continue
		}
		sub := s.witnessChain(d.Delegator, d.Subset, ctx, visiting)
		out = append(out, d.ID)
		out = append(out, sub...)
		remaining = remaining.Subtract(d.Subset)
	}
	return out
}
