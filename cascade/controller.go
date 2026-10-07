// Package cascade 实现一个带属主引用与终结器的级联删除控制器。
//
// 控制器复刻 Kubernetes 垃圾收集语义的核心规则：后台 / 前台 / 孤立
// 三种删除策略、多属主依赖者、终结器阻塞，以及前台删除的传播。
// 所有改变状态的操作在返回前都会把级联结果收敛到唯一稳定状态。
package cascade

import (
	"sort"
	"sync"
)

// Controller 是线程安全的级联删除控制器。
type Controller struct {
	mu sync.Mutex

	// objects 保存存活对象（含处于删除中的对象）。
	objects map[string]*object
	// dependents 是从属主 ID -> 该属主的依赖者集合。
	dependents map[string]map[string]struct{}

	clock int64
}

// object 是内部可变对象。
type object struct {
	id       string
	owners   []OwnerRef // 按 OwnerID 升序、无重复
	fins     map[string]struct{}
	deleting bool
	strategy Strategy
	deleteAt int64

	// 以下为增量维护的派生计数，使收敛时的判定为 O(1)，
	// 且一次操作的总开销只与其实际触达的对象/引用数相关。

	// blockingAliveDeps 是“仍存活且阻塞”的依赖者数量。
	blockingAliveDeps int
	// fgOwners 是处于前台删除中的属主数量。
	fgOwners int
	// aliveOwners 是仍存活（未被移除）的属主数量。
	aliveOwners int
	// deletingOwners 是存活且处于删除中（任意策略）的属主数量。
	deletingOwners int
}

// New 创建空控制器。
func New() *Controller {
	return &Controller{
		objects:    map[string]*object{},
		dependents: map[string]map[string]struct{}{},
	}
}

// Create 创建一个对象。所有属主必须已存在且不处于删除中。
// 新对象自身存活，故创建不会触发任何级联。
func (c *Controller) Create(id string, owners []OwnerRef, finalizers []string) error {
	if id == "" {
		return newError(KindInvalidArgument, "object id must not be empty")
	}
	for _, name := range finalizers {
		if name == "" {
			return newError(KindInvalidArgument, "finalizer name must not be empty")
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.objects[id]; exists {
		return newError(KindInvalidArgument, "object already exists: "+id)
	}
	refs, err := normalizeOwners(c, id, owners, true)
	if err != nil {
		return err
	}

	finSet := make(map[string]struct{}, len(finalizers))
	for _, name := range finalizers {
		finSet[name] = struct{}{}
	}

	obj := &object{
		id:          id,
		owners:      refs,
		fins:        finSet,
		aliveOwners: len(refs),
	}
	c.objects[id] = obj
	for _, ref := range refs {
		deps := c.dependents[ref.OwnerID]
		if deps == nil {
			deps = map[string]struct{}{}
			c.dependents[ref.OwnerID] = deps
		}
		deps[id] = struct{}{}
		c.objects[ref.OwnerID].blockingAliveDeps += boolToInt(ref.Blocking)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Delete 发起删除。at 为请求时刻（调用方负责单调推进；控制器内部
// 级联使用自己的单调时钟）。重复删除是成功的无操作，仅允许
// Background -> Foreground 升级。
func (c *Controller) Delete(id string, strategy Strategy) error {
	if id == "" {
		return newError(KindInvalidArgument, "object id must not be empty")
	}
	if strategy != Background && strategy != Foreground && strategy != Orphan {
		return newError(KindInvalidArgument, "invalid delete strategy")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	obj, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object does not exist: "+id)
	}

	c.clock++
	if obj.deleting {
		// 已删除中：仅后台 -> 前台升级才需要重新收敛。
		if obj.strategy == Background && strategy == Foreground {
			eng := newEngine(c)
			eng.settleFrom(eng.markDeleting(id, Foreground))
		}
		return nil
	}

	eng := newEngine(c)
	eng.settleFrom(eng.markDeleting(id, strategy))
	return nil
}

// AddFinalizer 追加终结器；同名无操作，删除中对象拒绝追加。
func (c *Controller) AddFinalizer(id, name string) error {
	if id == "" || name == "" {
		return newError(KindInvalidArgument, "id and finalizer name must not be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object does not exist: "+id)
	}
	if obj.deleting {
		return newError(KindConflict, "cannot add finalizer to deleting object: "+id)
	}
	obj.fins[name] = struct{}{}
	return nil
}

// RemoveFinalizer 移除终结器；不存在报错。移除最后一个终结器可能
// 立即触发该对象及其整条级联的移除。
func (c *Controller) RemoveFinalizer(id, name string) error {
	if id == "" || name == "" {
		return newError(KindInvalidArgument, "id and finalizer name must not be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object does not exist: "+id)
	}
	if _, exists := obj.fins[name]; !exists {
		return newError(KindNotFound, "finalizer does not exist: "+name)
	}
	delete(obj.fins, name)
	c.clock++
	newEngine(c).settle([]string{id})
	return nil
}

// ReplaceOwners 替换属主引用集合；删除中对象不得修改属主。
func (c *Controller) ReplaceOwners(id string, owners []OwnerRef) error {
	if id == "" {
		return newError(KindInvalidArgument, "object id must not be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, ok := c.objects[id]
	if !ok {
		return newError(KindNotFound, "object does not exist: "+id)
	}
	if obj.deleting {
		return newError(KindConflict, "cannot replace owners of deleting object: "+id)
	}
	refs, err := normalizeOwners(c, id, owners, false)
	if err != nil {
		return err
	}

	seed := map[string]struct{}{id: {}}
	for _, ref := range obj.owners {
		seed[ref.OwnerID] = struct{}{}
	}
	for _, ref := range refs {
		seed[ref.OwnerID] = struct{}{}
	}
	c.relink(obj, refs)
	// 属主边变化可能解除某个删除中对象的阻塞条件；任何改变状态的
	// 操作返回前都必须收敛，故把该对象及其新旧属主作为种子收敛。
	newEngine(c).settleFrom(seed)
	return nil
}

// relink 在属主集合已校验通过后更新边与派生计数。被替换对象存活，
// 不做级联；新属主均存活、旧属主边被摘除。
func (c *Controller) relink(obj *object, refs []OwnerRef) {
	old := map[string]OwnerRef{}
	for _, ref := range obj.owners {
		old[ref.OwnerID] = ref
	}
	new := map[string]OwnerRef{}
	for _, ref := range refs {
		new[ref.OwnerID] = ref
	}

	// 属主侧索引与 blockingAliveDeps：对仍存活的旧属主应用
	// “删除 / 阻塞标志变更 / 新增”的对称差。只触碰真实存在的属主，
	// 不依赖对象自身可能残留的历史计数。
	for oid, ref := range old {
		owner := c.objects[oid]
		if owner == nil {
			continue
		}
		if nr, keep := new[oid]; keep {
			if ref.Blocking != nr.Blocking {
				if nr.Blocking {
					owner.blockingAliveDeps++
				} else {
					owner.blockingAliveDeps--
				}
			}
			continue
		}
		if deps := c.dependents[oid]; deps != nil {
			delete(deps, obj.id)
			if len(deps) == 0 {
				delete(c.dependents, oid)
			}
		}
		if ref.Blocking {
			owner.blockingAliveDeps--
		}
	}
	for oid, ref := range new {
		if _, had := old[oid]; had {
			continue
		}
		deps := c.dependents[oid]
		if deps == nil {
			deps = map[string]struct{}{}
			c.dependents[oid] = deps
		}
		deps[obj.id] = struct{}{}
		if ref.Blocking {
			c.objects[oid].blockingAliveDeps++
		}
	}

	// 对象自身的派生计数直接按新引用重建：校验已保证每个新属主都存在
	// 且不处于删除中，故新集合不含任何删除中属主，相关计数必为 0。
	obj.aliveOwners = len(refs)
	obj.fgOwners = 0
	obj.deletingOwners = 0
	obj.owners = refs
}

// Exists 判断对象是否仍存活（未被移除）。
func (c *Controller) Exists(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.objects[id]
	return ok
}

// Get 返回对象只读快照。
func (c *Controller) Get(id string) (Object, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, ok := c.objects[id]
	if !ok {
		return Object{}, false
	}
	return c.snapshotObject(obj), true
}

func (c *Controller) snapshotObject(obj *object) Object {
	owners := append([]OwnerRef(nil), obj.owners...)
	fins := make([]string, 0, len(obj.fins))
	for name := range obj.fins {
		fins = append(fins, name)
	}
	sort.Strings(fins)
	return Object{
		ID:         obj.id,
		Owners:     owners,
		Finalizers: fins,
		Deleting:   obj.deleting,
		Strategy:   obj.strategy,
		DeleteAt:   obj.deleteAt,
	}
}

// Snapshot 返回完整只读快照。
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Snapshot{Objects: map[string]Object{}, Clock: c.clock}
	for id, obj := range c.objects {
		out.Objects[id] = c.snapshotObject(obj)
	}
	return out
}
