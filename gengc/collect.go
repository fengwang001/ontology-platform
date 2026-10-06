package gengc

import "sort"

// liveYoungLocked 计算一次年轻区回收的存活年轻集合。
// 起点（两者都视为根）：
//  1. 根集合直接引用的年轻对象；
//  2. 记忆集中每个年老对象的字段所引用的年轻对象。
//
// 只在年轻对象之间继续传播：年老目标不展开（本次不收集年老区），
// 因此开销只与年轻区对象数、根数、记忆集大小（及其字段）相关。
func (h *Heap) liveYoungLocked() map[uint64]struct{} {
	live := make(map[uint64]struct{})
	stack := make([]uint64, 0)

	seed := func(id uint64) {
		if id == 0 {
			return
		}
		o, ok := h.objects[id]
		if !ok || o.gen != GenYoung {
			return
		}
		if _, seen := live[id]; seen {
			return
		}
		live[id] = struct{}{}
		stack = append(stack, id)
	}

	// 根直接引用的年轻对象（只访问根，不扫描任何年老对象）。
	for id := range h.roots {
		seed(id)
	}

	// 记忆集成员引用的年轻对象：只访问记忆集成员，不访问其他年老对象。
	for id := range h.rs.members {
		o, ok := h.objects[id]
		if !ok || o.gen != GenOld {
			continue
		}
		for _, ref := range o.fields {
			seed(ref)
		}
	}

	// 仅沿年轻对象继续追踪。
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, ref := range h.objects[cur].fields {
			seed(ref)
		}
	}
	return live
}

// collectYoungLocked 执行一次年轻区回收：
// 存活判定 → 计数/晋升（晋升在判定之后，死对象不晋升）→ 清扫 → 重算记忆集。
func (h *Heap) collectYoungLocked(speculative bool) {
	live := h.liveYoungLocked()

	// 按 ID 固定顺序处理，保证“晋升了几次/哪些对象”有客观唯一答案。
	ids := make([]uint64, 0, len(live))
	for id := range live {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	promoted := make(map[uint64]struct{})
	for _, id := range ids {
		o := h.objects[id]
		if o.youngGCs < h.promoteAt {
			o.youngGCs++
		}
		if o.youngGCs >= h.promoteAt && h.oldCountLocked() < h.oldCap {
			o.gen = GenOld
			o.youngGCs = h.promoteAt
			o.promotions++
			h.promotions++
			promoted[id] = struct{}{}
		}
		// 晋升失败：留在年轻区，次数钳制在阈值，下次回收再次尝试。
		if o.gen == GenYoung && o.youngGCs > h.promoteAt {
			o.youngGCs = h.promoteAt
		}
	}

	// 清扫死亡年轻对象；任何存活对象（含年老记忆集成员与新晋升者）
	// 指向它们的槽位置空，保证不存在指向墓碑的引用。
	dead := make(map[uint64]struct{})
	for id, o := range h.objects {
		if o.gen == GenYoung {
			if _, alive := live[id]; !alive {
				dead[id] = struct{}{}
			}
		}
	}

	clearRefsTo := func(o *Object) {
		for i, ref := range o.fields {
			if _, bad := dead[ref]; bad {
				o.fields[i] = 0
			}
		}
	}
	for _, id := range ids {
		if o, ok := h.objects[id]; ok && o.gen == GenYoung {
			clearRefsTo(o)
		}
	}
	for id := range h.rs.members {
		if o, ok := h.objects[id]; ok {
			clearRefsTo(o)
		}
	}
	for id := range promoted {
		clearRefsTo(h.objects[id])
	}
	for id := range dead {
		h.tomb[id] = struct{}{}
		delete(h.objects, id)
	}

	// 重算记忆集（不沿用）：候选 = 旧记忆集成员 ∪ 本次晋升者。
	// 仍引用存活年轻对象者保留（晋升者首次加入），其余移除。
	// 年轻→年老的跨区指针无需记忆集（年轻区回收会完整扫描年轻存活集）。
	candidates := make([]uint64, 0, len(h.rs.members)+len(promoted))
	seen := make(map[uint64]struct{}, cap(candidates))
	addCandidate := func(id uint64) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		candidates = append(candidates, id)
	}
	for id := range h.rs.members {
		addCandidate(id)
	}
	for id := range promoted {
		addCandidate(id)
	}

	newRS := make(map[uint64]struct{})
	for _, id := range candidates {
		o, ok := h.objects[id]
		if !ok || o.gen != GenOld {
			continue
		}
		for _, ref := range o.fields {
			if ref == 0 {
				continue
			}
			target, ok := h.objects[ref]
			if ok && target.gen == GenYoung {
				newRS[id] = struct{}{}
				break
			}
		}
	}
	h.rs.members = newRS

	h.youngGCs++
	_ = speculative
}

// collectOldLocked 执行一次年老区回收：扫描整个托管空间，从根集合
// 判定可达性，清除死亡年老对象及其记忆集登记项，并悬空指向它们的槽。
// 不改变任何对象的年轻区回收次数；不回收死亡年轻对象。
func (h *Heap) collectOldLocked() {
	live := h.reachableLocked()

	deadOld := make(map[uint64]struct{})
	for id, o := range h.objects {
		if o.gen == GenOld {
			if _, alive := live[id]; !alive {
				deadOld[id] = struct{}{}
			}
		}
	}

	for id := range deadOld {
		h.rs.remove(id)
		h.tomb[id] = struct{}{}
		delete(h.objects, id)
	}
	for _, o := range h.objects {
		for i, ref := range o.fields {
			if _, bad := deadOld[ref]; bad {
				o.fields[i] = 0
			}
		}
	}

	h.oldGCs++
}
