package cascade

import "sort"

// normalizeOwners 校验并规范化一组属主引用：非空、不自引用、不重复、
// 属主存在且不处于删除中、合入后图不成环。
//
// 错误严格按优先级返回：
// 参数非法（空 ID、非法策略、重复引用等）> 对象不存在 >
// 状态冲突 > 环 > 属主缺失。
// 属主“处于删除中”按属主缺失处理（它已不能再被引用）。
func normalizeOwners(c *Controller, selfID string, refs []OwnerRef, forCreate bool) ([]OwnerRef, error) {
	seen := map[string]struct{}{}
	out := make([]OwnerRef, 0, len(refs))
	for _, ref := range refs {
		if ref.OwnerID == "" {
			return nil, newError(KindInvalidArgument, "owner id must not be empty")
		}
		if ref.OwnerID == selfID {
			return nil, newError(KindCycle, "object must not reference itself")
		}
		if _, dup := seen[ref.OwnerID]; dup {
			return nil, newError(KindInvalidArgument, "duplicate owner reference: "+ref.OwnerID)
		}
		seen[ref.OwnerID] = struct{}{}
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OwnerID < out[j].OwnerID })

	// 环检测优先于属主缺失：沿“新引用 + 现存引用”向上游走，
	// 缺失属主视为无出边的终点。若从 selfID 出发回到 selfID 即成环。
	if introducesCycle(c, selfID, out) {
		return nil, newError(KindCycle, "owner chain forms a cycle")
	}

	for _, ref := range out {
		owner, ok := c.objects[ref.OwnerID]
		if !ok {
			return nil, newError(KindOwnerMissing, "owner does not exist: "+ref.OwnerID)
		}
		if owner.deleting {
			return nil, newError(KindOwnerMissing, "owner is being deleted: "+ref.OwnerID)
		}
	}
	return out, nil
}

// introducesCycle 判断把 selfID 的属主集合替换为 newOwners 后，
// 是否产生环。只需从 selfID 沿属主方向做一次 DFS：
// 若能再次到达 selfID 则成环。缺失属主按无出边处理，
// 因而“成环”与“属主缺失”并存时仍可稳定地报环。
func introducesCycle(c *Controller, selfID string, newOwners []OwnerRef) bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{selfID: gray}

	var visit func(id string) bool
	visit = func(id string) bool {
		var refs []OwnerRef
		if id == selfID {
			refs = newOwners
		} else if obj, ok := c.objects[id]; ok {
			refs = obj.owners
		}
		for _, ref := range refs {
			next := ref.OwnerID
			if next == selfID {
				return true
			}
			if color[next] != white {
				if color[next] == gray {
					return true
				}
				continue
			}
			color[next] = gray
			if visit(next) {
				return true
			}
			color[next] = black
		}
		return false
	}
	return visit(selfID)
}
