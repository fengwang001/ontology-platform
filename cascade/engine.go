package cascade

import "sort"

// engine 承载收敛算法。它本身不加锁，由 Controller 的公开方法在持有
// c.mu 时调用，因此天然满足“任意并发等价于某个串行顺序”以及
// “每次删除的级联对外不可分割”。
type engine struct {
	c *Controller
}

func newEngine(c *Controller) *engine {
	return &engine{c: c}
}

// settle 从候选集合收敛到唯一稳定状态。
func (e *engine) settle(seed []string) {
	dirty := make(map[string]struct{}, len(seed))
	for _, id := range seed {
		dirty[id] = struct{}{}
	}
	e.settleFrom(dirty)
}

// settleFrom 采用“传播 → 移除”两遍式不动点：
//  1. 前台传播闭包：在不真正移除任何对象的前提下，把所有满足
//     “仍存活且全部存活属主都处于前台删除中”的对象统一标记为前台
//     删除。传播先于移除，因此属主是否会在稍后被移除，都不影响
//     依赖者本应看到的“属主删除中”窗口——这正是顺序无关性的关键。
//  2. 移除：反复移除所有满足移除条件的对象，按策略摘除依赖者边，
//     并对零属主依赖者做后台连带删除。
//
// 移除会改变属主存活集合，可能开启新的传播窗口，故整体循环到稳定。
// 所有变化都是单调的（进入删除中、被移除；策略仅 Background ->
// Foreground 升级），不动点唯一，与规则触发先后无关。
//
// 每个对象只在它或它相邻的边发生变化时才被处理，故一次操作的总开销
// 为 O(被影响对象数 + 被触达引用数)，与无关对象总数无关。
func (e *engine) settleFrom(initial map[string]struct{}) {
	dirty := map[string]struct{}{}
	for id := range initial {
		dirty[id] = struct{}{}
	}
	for {
		propChanged := e.propagateForeground(dirty)
		// propagateForeground 会把新进入前台删除的对象加入 dirty，
		// 使本轮 removeLoop 能立刻评估并移除其中无阻塞、无终结器者。
		removeChanged := e.removeLoop(dirty)
		// removeLoop 已把受影响对象 enqueue 处理，但下一轮传播需要从
		// 同一集合重新开始：dirty 在 removeLoop 内被原地补充。
		if !propChanged && !removeChanged {
			return
		}
	}
}

// propagateForeground 从候选区域沿 dependents 边做前台传播闭包。
// 返回是否有对象因此进入前台删除。
func (e *engine) propagateForeground(dirty map[string]struct{}) bool {
	changed := false
	work := make([]string, 0, len(dirty))
	for id := range dirty {
		if e.c.objects[id] != nil {
			work = append(work, id)
		}
	}
	sort.Strings(work)
	inWork := map[string]struct{}{}

	for len(work) > 0 {
		id := work[0]
		work = work[1:]
		delete(inWork, id)
		obj := e.c.objects[id]
		if obj == nil {
			continue
		}

		// 对候选对象本身评估传播条件（候选可能存活，也可能是刚被连带
		// 后台删除者）。
		if (!obj.deleting || obj.strategy == Background) &&
			obj.aliveOwners > 0 && obj.fgOwners > 0 &&
			obj.deletingOwners == obj.aliveOwners {
			for t := range e.markDeleting(id, Foreground) {
				changed = true
				dirty[t] = struct{}{}
				if t != id && e.c.objects[t] != nil {
					if _, q := inWork[t]; !q {
						work = append(work, t)
						inWork[t] = struct{}{}
					}
				}
			}
			obj = e.c.objects[id]
			if obj == nil {
				continue
			}
		}

		// 前台删除中节点：沿其依赖者边展开传播闭包。
		if !obj.deleting || obj.strategy != Foreground {
			continue
		}
		deps := make([]string, 0, len(e.c.dependents[id]))
		for depID := range e.c.dependents[id] {
			deps = append(deps, depID)
		}
		sort.Strings(deps)
		for _, depID := range deps {
			if e.c.objects[depID] == nil {
				continue
			}
			if _, q := inWork[depID]; !q {
				work = append(work, depID)
				inWork[depID] = struct{}{}
			}
		}
	}
	return changed
}

// removeLoop 反复移除所有当前可移除的对象，直到一轮内没有移除发生。
// 返回是否至少移除了一个对象。
func (e *engine) removeLoop(dirty map[string]struct{}) bool {
	changed := false
	queue := make([]string, 0, len(dirty))
	inQueue := map[string]struct{}{}
	enqueue := func(id string) {
		if e.c.objects[id] == nil {
			return
		}
		if _, q := inQueue[id]; q {
			return
		}
		inQueue[id] = struct{}{}
		queue = append(queue, id)
		dirty[id] = struct{}{}
	}
	seeds := make([]string, 0, len(dirty))
	for id := range dirty {
		seeds = append(seeds, id)
	}
	for _, id := range seeds {
		enqueue(id)
	}

	for len(queue) > 0 {
		sort.Strings(queue)
		id := queue[0]
		queue = queue[1:]
		delete(inQueue, id)

		obj := e.c.objects[id]
		if obj == nil || !obj.deleting || !e.canRemove(obj) {
			continue
		}
		touched := map[string]struct{}{}
		e.removeObject(obj, touched)
		changed = true
		for t := range touched {
			enqueue(t)
		}
	}
	return changed
}

// canRemove 判定删除中对象是否立即可移除。
func (e *engine) canRemove(obj *object) bool {
	if len(obj.fins) > 0 {
		return false
	}
	if obj.strategy == Foreground && obj.blockingAliveDeps > 0 {
		return false
	}
	return true
}

// markDeleting 把对象置为删除中；已删除中时仅做 Background -> Foreground
// 的单调升级。返回因此受到影响、需要重新评估的对象集合（含自身与
// 直接依赖者）。它只更新派生计数与状态，不做移除。
func (e *engine) markDeleting(id string, strategy Strategy) map[string]struct{} {
	affected := map[string]struct{}{}
	obj := e.c.objects[id]
	if obj == nil {
		return affected
	}
	upgrade := obj.deleting && obj.strategy == Background && strategy == Foreground
	if obj.deleting && !upgrade {
		return affected
	}
	wasDeleting := obj.deleting
	obj.deleting = true
	obj.strategy = strategy
	obj.deleteAt = e.c.clock
	affected[id] = struct{}{}

	if strategy == Foreground {
		// 成为前台删除中：每个存活依赖者都多了一个前台删除中的属主。
		// 是否传播由 propagateForeground 统一闭包计算，这里只维护计数，
		// 从而把“计数维护”与“传播判定”两件事彻底分开。
		for depID := range e.c.dependents[id] {
			if dep := e.c.objects[depID]; dep != nil {
				dep.fgOwners++
				affected[depID] = struct{}{}
			}
		}
	}

	// 只有“存活 -> 删除中”的真实翻转才增加依赖者的 deletingOwners；
	// Background -> Foreground 升级并不改变“属主是否删除中”。
	if !wasDeleting {
		for depID := range e.c.dependents[id] {
			if dep := e.c.objects[depID]; dep != nil {
				dep.deletingOwners++
				affected[depID] = struct{}{}
			}
		}
	}
	return affected
}

// removeObject 真正移除对象，更新所有相邻边与派生计数，并把受影响对象
// 推入 touched 供后续评估。
func (e *engine) removeObject(obj *object, touched map[string]struct{}) {
	id := obj.id

	// 1. 从每个存活属主的依赖者索引上摘除自己；阻塞引用会减少
	// 属主“仍存活且阻塞”的依赖者计数。
	for _, ref := range obj.owners {
		owner := e.c.objects[ref.OwnerID]
		if owner == nil {
			continue
		}
		if deps := e.c.dependents[ref.OwnerID]; deps != nil {
			delete(deps, id)
			if len(deps) == 0 {
				delete(e.c.dependents, ref.OwnerID)
			}
		}
		if ref.Blocking {
			owner.blockingAliveDeps--
		}
		touched[ref.OwnerID] = struct{}{}
	}

	// 2. 对每个依赖者先摘除指向本对象的那条引用，再按策略决定连带。
	deps := e.c.dependents[id]
	depIDs := make([]string, 0, len(deps))
	for depID := range deps {
		depIDs = append(depIDs, depID)
	}
	sort.Strings(depIDs)
	for _, depID := range depIDs {
		dep := e.c.objects[depID]
		if dep == nil {
			continue
		}
		idx := indexOfOwner(dep.owners, id)
		if idx < 0 {
			continue
		}
		blocking := dep.owners[idx].Blocking
		dep.owners = removeOwnerAt(dep.owners, idx)
		dep.aliveOwners--
		dep.deletingOwners-- // 能到这里属主必处于删除中
		if obj.strategy == Foreground {
			dep.fgOwners--
		}
		if blocking {
			obj.blockingAliveDeps--
		}

		touched[depID] = struct{}{}

		if obj.strategy != Orphan {
			// 后台/前台属主移除：摘除后一个属主也不剩的“仍存活”
			// 依赖者连带进入后台删除。已删除中的依赖者策略不变。
			if len(dep.owners) == 0 && !dep.deleting {
				for t := range e.markDeleting(depID, Background) {
					touched[t] = struct{}{}
				}
			}
		}
	}
	delete(e.c.dependents, id)

	// 3. 从对象表移除。
	delete(e.c.objects, id)
}

func indexOfOwner(owners []OwnerRef, ownerID string) int {
	for i, ref := range owners {
		if ref.OwnerID == ownerID {
			return i
		}
	}
	return -1
}

func removeOwnerAt(owners []OwnerRef, i int) []OwnerRef {
	out := make([]OwnerRef, 0, len(owners)-1)
	out = append(out, owners[:i]...)
	out = append(out, owners[i+1:]...)
	return out
}
