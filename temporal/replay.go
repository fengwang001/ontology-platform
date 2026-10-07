package temporal

// replayResult 是对某个 (主体, 标签) 的变更流做双轴重放的结果。
type replayResult struct {
	// alive 表示每条变更在新基线下是否仍然成立。
	alive map[string]bool
	// basis 是据以裁决的完整时序依据（含被追溯撤销的撤销记录）。
	basis []string
	// latest 是最后一条按全序存活的 Grant/Deny；空串表示无规则（默认拒绝）。
	latest  string
	allowed bool
	hasRule bool
}

// replay 对已按 OrderKey 全序排好的变更流做单遍重放。
//
// 规则：
//   - Grant/Deny 在且仅当其 DependsOn 前置此刻仍存活时成立；
//   - Revoke 在且仅当其目标此刻存活时成立；成立即杀死目标，并沿
//     “谁依赖了谁”的反向边传递地、确定地杀死全部受牵连的后续变更；
//   - 最终裁决取最后一条存活的 Grant/Deny；没有则默认拒绝。
//
// 重放只依赖输入变更集合与其固有 ID，因而对相同集合必然可重复。
func replay(ordered []Change) replayResult {
	alive := map[string]bool{}
	revDeps := map[string][]string{}
	for _, c := range ordered {
		if c.DependsOn != "" {
			revDeps[c.DependsOn] = append(revDeps[c.DependsOn], c.ID)
		}
	}

	var basis []string
	kill := func(root string) {
		queue := []string{root}
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			if !alive[x] {
				continue
			}
			alive[x] = false
			queue = append(queue, revDeps[x]...)
		}
	}

	for _, c := range ordered {
		switch c.Kind {
		case Grant, Deny:
			if c.DependsOn != "" && !alive[c.DependsOn] {
				alive[c.ID] = false
				continue
			}
			alive[c.ID] = true
			basis = append(basis, c.ID)
		case Revoke:
			if !alive[c.Target] {
				alive[c.ID] = false
				continue
			}
			alive[c.ID] = true
			kill(c.Target)
			basis = append(basis, c.ID+">revoke>"+c.Target)
		default:
			alive[c.ID] = false
		}
	}

	res := replayResult{alive: alive, basis: basis}
	for i := len(ordered) - 1; i >= 0; i-- {
		c := ordered[i]
		if (c.Kind == Grant || c.Kind == Deny) && alive[c.ID] {
			res.latest = c.ID
			res.allowed = c.Kind == Grant
			res.hasRule = true
			break
		}
	}
	return res
}
