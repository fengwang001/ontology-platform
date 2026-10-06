package gc

// 属主引用校验拆成四级，与错误优先级一一对应，便于各操作把自身的
// 「对象不存在 / 状态冲突」检查插到正确的优先级位置：
//
//	参数非法 > 对象不存在 > 状态冲突 > 环 > 属主缺失
//
// 校验失败时不改变任何状态。

// checkRefArgs 参数非法：属主标识为空、同一属主重复引用。
func (c *Controller) checkRefArgs(targetID string, refs []OwnerRef) *Error {
	seen := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		c.steps++
		if r.OwnerID == "" {
			return newError(KindInvalidArgument, "owner id must not be empty (target %q)", targetID)
		}
		if _, dup := seen[r.OwnerID]; dup {
			return newError(KindInvalidArgument, "duplicate owner reference %q on %q", r.OwnerID, targetID)
		}
		seen[r.OwnerID] = struct{}{}
	}
	return nil
}

// checkOwnersNotDeleting 状态冲突：某个已存在的属主处于删除中。
func (c *Controller) checkOwnersNotDeleting(targetID string, refs []OwnerRef) *Error {
	for _, r := range refs {
		c.steps++
		if owner, ok := c.objects[r.OwnerID]; ok && owner.deleting {
			return newError(KindConflict, "owner %q of %q is being deleted", r.OwnerID, targetID)
		}
	}
	return nil
}

// checkNoCycle 环：自引用（视为长度为 1 的环）或经既有属主链回到 targetID。
// targetID 为引用发起方（创建时可为尚不存在的对象标识）。
func (c *Controller) checkNoCycle(targetID string, refs []OwnerRef) *Error {
	for _, r := range refs {
		c.steps++
		if r.OwnerID == targetID {
			return newError(KindCycle, "object %q must not reference itself", targetID)
		}
		if c.reachableThroughOwners(r.OwnerID, targetID) {
			return newError(KindCycle, "owner reference %q -> %q would create a cycle", targetID, r.OwnerID)
		}
	}
	return nil
}

// checkOwnersExist 属主缺失：引用了不存在的对象。
func (c *Controller) checkOwnersExist(targetID string, refs []OwnerRef) *Error {
	for _, r := range refs {
		c.steps++
		if _, ok := c.objects[r.OwnerID]; !ok {
			return newError(KindOwnerMissing, "owner %q of %q does not exist", r.OwnerID, targetID)
		}
	}
	return nil
}

// reachableThroughOwners 报告从 from 出发沿属主链（owner -> owner 的 owner ...）
// 能否到达 target。只访问 from 的祖先链，开销与无关对象总数无关。
func (c *Controller) reachableThroughOwners(from, target string) bool {
	stack := []string{from}
	visited := map[string]struct{}{from: {}}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		c.steps++
		o, ok := c.objects[id]
		if !ok {
			continue
		}
		for ownerID := range o.owners {
			c.steps++
			if ownerID == target {
				return true
			}
			if _, seen := visited[ownerID]; !seen {
				visited[ownerID] = struct{}{}
				stack = append(stack, ownerID)
			}
		}
	}
	return false
}
