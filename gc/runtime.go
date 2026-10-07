package gc

import "sync"

// Runtime 是分代内存回收模型的入口。所有变更操作在 rt.mu 下串行化，
// 因此并发执行的效果必然等价于某个串行顺序。
type Runtime struct {
	mu sync.Mutex

	objs      map[ObjID]*object
	nextID    ObjID
	roots     map[ObjID]struct{}
	remset    rememberedSet
	youngN    int
	oldN      int
	youngCap  int
	oldCap    int
	threshold int

	promotions    uint64
	minorGCs      uint64
	majorGCs      uint64
	barrierRegs   uint64
	lastMinorWork int
}

// New 构造运行时。youngCap/oldCap 为两区可容纳的最大对象数，
// threshold 为晋升阈值（熬过的年轻区回收次数达到该值即晋升）。
func New(youngCap, oldCap, threshold int) *Runtime {
	if youngCap < 0 || oldCap < 0 || threshold < 1 {
		panic("gc: invalid runtime configuration")
	}
	return &Runtime{
		objs:      make(map[ObjID]*object),
		roots:     make(map[ObjID]struct{}),
		remset:    make(rememberedSet),
		youngCap:  youngCap,
		oldCap:    oldCap,
		threshold: threshold,
	}
}

// Stats 是同一瞬间（持锁）采集的统计视图。
type Stats struct {
	YoungObjects         int
	OldObjects           int
	Promotions           uint64
	MinorGCs             uint64
	MajorGCs             uint64
	RememberedSize       int
	BarrierRegistrations uint64
	// LastMinorWork 是上一次年轻区回收扫描的工作量（根数 + 记忆集扫描数 +
	// 标记的年轻对象数），用于验证其开销与年老区对象数无关。
	LastMinorWork int
}

func (rt *Runtime) Stats() Stats {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return Stats{
		YoungObjects:         rt.youngN,
		OldObjects:           rt.oldN,
		Promotions:           rt.promotions,
		MinorGCs:             rt.minorGCs,
		MajorGCs:             rt.majorGCs,
		RememberedSize:       len(rt.remset),
		BarrierRegistrations: rt.barrierRegs,
		LastMinorWork:        rt.lastMinorWork,
	}
}

// Alloc 在年轻区分配一个含 numFields 个引用字段的对象。
// 年轻区不足时先自动触发年轻区回收，仍不足则报 ErrOutOfMemory，
// 该次分配不产生任何对象。
func (rt *Runtime) Alloc(numFields int) (ObjID, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if numFields < 0 {
		return NilObj, ErrBadArgument
	}
	if rt.youngN >= rt.youngCap {
		rt.minorGCLocked()
		if rt.youngN >= rt.youngCap {
			return NilObj, ErrOutOfMemory
		}
	}
	rt.nextID++
	o := &object{id: rt.nextID, gen: genYoung, fields: make([]ObjID, numFields), alive: true}
	rt.objs[o.id] = o
	rt.youngN++
	return o.id, nil
}

// Write 将 src 的第 field 个字段改写为 dst（dst 为 NilObj 表示清除引用）。
// 校验次序：未定义 > 参数错误 > 悬垂引用。被拒绝时不改动任何状态。
func (rt *Runtime) Write(srcID ObjID, field int, dstID ObjID) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	src, ok := rt.objs[srcID]
	if !ok {
		return ErrUndefined
	}
	var dst *object
	if dstID != NilObj {
		dst, ok = rt.objs[dstID]
		if !ok {
			return ErrUndefined
		}
	}
	if field < 0 || field >= len(src.fields) {
		return ErrBadArgument
	}
	if !src.alive || (dst != nil && !dst.alive) {
		return ErrDangling
	}
	src.fields[field] = dstID
	if dst != nil {
		rt.writeBarrierLocked(src, dst)
	}
	return nil
}

// Read 读取 src 的第 field 个字段，校验次序与 Write 相同。
func (rt *Runtime) Read(srcID ObjID, field int) (ObjID, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	src, ok := rt.objs[srcID]
	if !ok {
		return NilObj, ErrUndefined
	}
	if field < 0 || field >= len(src.fields) {
		return NilObj, ErrBadArgument
	}
	if !src.alive {
		return NilObj, ErrDangling
	}
	return src.fields[field], nil
}

// AddRoot 把对象加入根集合。
func (rt *Runtime) AddRoot(id ObjID) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	o, ok := rt.objs[id]
	if !ok {
		return ErrUndefined
	}
	if !o.alive {
		return ErrDangling
	}
	rt.roots[id] = struct{}{}
	return nil
}

// RemoveRoot 把对象移出根集合；对象不会立即死亡，下次回收时按可达性判定。
func (rt *Runtime) RemoveRoot(id ObjID) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, ok := rt.objs[id]; !ok {
		return ErrUndefined
	}
	delete(rt.roots, id)
	return nil
}

// MinorGC 显式触发一次年轻区回收。
func (rt *Runtime) MinorGC() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.minorGCLocked()
}

// MajorGC 显式触发一次年老区回收（只可能由显式触发）。
func (rt *Runtime) MajorGC() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.majorGCLocked()
}

// minorGCLocked / majorGCLocked 分别在 minor.go / major.go 中实现。
