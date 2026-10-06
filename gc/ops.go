package gc

import "time"

// 公开操作。每个操作：持锁 -> 重置步数计数 -> 按优先级校验 ->
// 修改状态 -> settle 收敛 -> 返回。被拒绝的操作不改变任何状态。

// Create 创建对象。全部属主必须已存在且不处于删除中；
// 引用不得重复、不得自引用、不得成环。finalizers 中重复的名字按一个计。
func (c *Controller) Create(id string, owners []OwnerRef, finalizers []string) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = 0

	if id == "" {
		return newError(KindInvalidArgument, "object id must not be empty")
	}
	for _, f := range finalizers {
		if f == "" {
			return newError(KindInvalidArgument, "finalizer name must not be empty (object %q)", id)
		}
	}
	if err := c.checkRefArgs(id, owners); err != nil {
		return err
	}
	if _, exists := c.objects[id]; exists {
		return newError(KindConflict, "object %q already exists", id)
	}
	if err := c.checkOwnersNotDeleting(id, owners); err != nil {
		return err
	}
	if err := c.checkNoCycle(id, owners); err != nil {
		return err
	}
	if err := c.checkOwnersExist(id, owners); err != nil {
		return err
	}

	o := &object{id: id, owners: make(map[string]bool, len(owners))}
	seenFin := make(map[string]struct{}, len(finalizers))
	for _, f := range finalizers {
		if _, dup := seenFin[f]; !dup {
			seenFin[f] = struct{}{}
			o.finalizers = append(o.finalizers, f)
		}
	}
	c.objects[id] = o
	for _, r := range owners {
		o.owners[r.OwnerID] = r.Block
		owner := c.objects[r.OwnerID] // 已校验存在且未删除
		if r.Block {
			owner.blockingDeps++
		}
		o.liveOwners++
		c.addDependent(r.OwnerID, id)
		c.steps++
	}
	return nil
}

// Delete 发起删除请求：只打删除中标记并记录策略与请求时刻，
// 随后收敛级联。对已删除中的对象是幂等成功，
// 仅「后台 -> 前台」会升级策略，其余组合不改变策略。
func (c *Controller) Delete(id string, policy Policy, now time.Time) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = 0

	if id == "" {
		return newError(KindInvalidArgument, "object id must not be empty")
	}
	if !policy.valid() {
		return newError(KindInvalidArgument, "unknown deletion policy %q", string(policy))
	}
	o, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object %q does not exist", id)
	}
	c.applyDeleteRequest(o, policy, now)
	c.settle(now)
	return nil
}

// AddFinalizer 追加终结器；同名已存在时是无操作成功。
// 处于删除中的对象不得追加新终结器。
func (c *Controller) AddFinalizer(id, name string) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = 0

	if id == "" || name == "" {
		return newError(KindInvalidArgument, "object id and finalizer name must not be empty")
	}
	o, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object %q does not exist", id)
	}
	if o.deleting {
		return newError(KindConflict, "object %q is being deleted; cannot add finalizer", id)
	}
	for _, f := range o.finalizers {
		if f == name {
			return nil // 无操作
		}
	}
	o.finalizers = append(o.finalizers, name)
	return nil
}

// RemoveFinalizer 移除终结器；不存在则报错。
// 移除最后一个终结器可能触发该对象及整条级联的移除，
// 返回时全部结果已经生效。
func (c *Controller) RemoveFinalizer(id, name string, now time.Time) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = 0

	if id == "" || name == "" {
		return newError(KindInvalidArgument, "object id and finalizer name must not be empty")
	}
	o, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object %q does not exist", id)
	}
	idx := -1
	for i, f := range o.finalizers {
		if f == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return newError(KindNotFound, "finalizer %q not present on object %q", name, id)
	}
	o.finalizers = append(o.finalizers[:idx], o.finalizers[idx+1:]...)
	c.remQ = append(c.remQ, o)
	c.settle(now)
	return nil
}

// SetOwners 整体替换对象的属主引用集合。新引用须满足存在、
// 不处于删除中、不成环、不自引用、不重复；删除中的对象不得修改属主。
// 替换后若对象满足前台传播条件（所有属主均删除中且有前台属主），
// 会在返回前收敛。
func (c *Controller) SetOwners(id string, refs []OwnerRef, now time.Time) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = 0

	if id == "" {
		return newError(KindInvalidArgument, "object id must not be empty")
	}
	if err := c.checkRefArgs(id, refs); err != nil {
		return err
	}
	o, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object %q does not exist", id)
	}
	if o.deleting {
		return newError(KindConflict, "object %q is being deleted; cannot modify owners", id)
	}
	if err := c.checkOwnersNotDeleting(id, refs); err != nil {
		return err
	}
	if err := c.checkNoCycle(id, refs); err != nil {
		return err
	}
	if err := c.checkOwnersExist(id, refs); err != nil {
		return err
	}

	newOwners := make(map[string]bool, len(refs))
	for _, r := range refs {
		newOwners[r.OwnerID] = r.Block
	}
	// 摘除旧引用：维护属主侧 blockingDeps、自身计数器与反向索引。
	for ownerID, block := range o.owners {
		c.steps++
		_, kept := newOwners[ownerID]
		owner := c.objects[ownerID]
		if block {
			owner.blockingDeps--
			c.remQ = append(c.remQ, owner) // 属主可能因此可移除
		}
		if !kept {
			if !owner.deleting {
				o.liveOwners--
			} else if owner.policy == Foreground {
				o.fgOwners--
			}
			c.removeDependent(ownerID, id)
		}
	}
	// 建立新引用（新增的属主已校验存在且未删除）。
	for ownerID, block := range newOwners {
		c.steps++
		_, existed := o.owners[ownerID]
		owner := c.objects[ownerID]
		if block {
			owner.blockingDeps++
		}
		if !existed {
			o.liveOwners++
			c.addDependent(ownerID, id)
		}
	}
	o.owners = newOwners
	// 替换后对象可能满足前台传播条件。
	c.propQ = append(c.propQ, o)
	c.settle(now)
	return nil
}
