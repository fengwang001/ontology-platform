package gengc

import "sync"

// Stats 是同一瞬间取样的统计视图。
type Stats struct {
	YoungObjects   int
	OldObjects     int
	Promotions     int
	YoungGCs       int
	OldGCs         int
	RememberedSize int
	BarrierEntries int
}

// Heap 是分代托管堆。所有公开方法通过同一把互斥锁串行化，
// 因此并发变更与回收严格等价于某个全局串行顺序；写屏障登记
// 与字段写入在同一临界区内完成，对回收不可分。
type Heap struct {
	mu sync.Mutex

	objects map[uint64]*Object
	tomb    map[uint64]struct{}
	roots   map[uint64]struct{}

	rs *rememberedSet

	youngCap  int
	oldCap    int
	promoteAt int
	nextID    uint64

	promotions int
	youngGCs   int
	oldGCs     int
}

// Config 为堆配置。PromoteThreshold<=0 时取默认值 2。
type Config struct {
	YoungCapacity    int
	OldCapacity      int
	PromoteThreshold int
}

const defaultPromoteThreshold = 2

// New 创建分代堆。
func New(cfg Config) *Heap {
	if cfg.PromoteThreshold <= 0 {
		cfg.PromoteThreshold = defaultPromoteThreshold
	}
	if cfg.YoungCapacity < 0 {
		cfg.YoungCapacity = 0
	}
	if cfg.OldCapacity < 0 {
		cfg.OldCapacity = 0
	}
	return &Heap{
		objects:   make(map[uint64]*Object),
		tomb:      make(map[uint64]struct{}),
		roots:     make(map[uint64]struct{}),
		rs:        newRememberedSet(),
		youngCap:  cfg.YoungCapacity,
		oldCap:    cfg.OldCapacity,
		promoteAt: cfg.PromoteThreshold,
		nextID:    1,
	}
}

// PromoteThreshold 返回晋升阈值（经历这么多次年轻区回收后在该次回收中晋升）。
func (h *Heap) PromoteThreshold() int { return h.promoteAt }

func (h *Heap) youngCountLocked() int {
	n := 0
	for _, o := range h.objects {
		if o.gen == GenYoung {
			n++
		}
	}
	return n
}

func (h *Heap) oldCountLocked() int {
	n := 0
	for _, o := range h.objects {
		if o.gen == GenOld {
			n++
		}
	}
	return n
}

// snapshot / restore 用于“空间不足则整次分配零副作用”的回滚保证：
// 先在快照上推测执行自动年轻区回收，回收后仍放不下就恢复原状。
type heapSnapshot struct {
	objects    map[uint64]*Object
	tomb       map[uint64]struct{}
	roots      map[uint64]struct{}
	rsMembers  map[uint64]struct{}
	rsBarriers int
	nextID     uint64
	promotions int
	youngGCs   int
	oldGCs     int
}

func (h *Heap) snapshotLocked() heapSnapshot {
	objs := make(map[uint64]*Object, len(h.objects))
	for id, o := range h.objects {
		objs[id] = o.clone()
	}
	tomb := make(map[uint64]struct{}, len(h.tomb))
	for id := range h.tomb {
		tomb[id] = struct{}{}
	}
	roots := make(map[uint64]struct{}, len(h.roots))
	for id := range h.roots {
		roots[id] = struct{}{}
	}
	members, barriers := h.rs.snapshot()
	return heapSnapshot{
		objects:    objs,
		tomb:       tomb,
		roots:      roots,
		rsMembers:  members,
		rsBarriers: barriers,
		nextID:     h.nextID,
		promotions: h.promotions,
		youngGCs:   h.youngGCs,
		oldGCs:     h.oldGCs,
	}
}

func (h *Heap) restoreLocked(s heapSnapshot) {
	h.objects = s.objects
	h.tomb = s.tomb
	h.roots = s.roots
	h.rs.restore(s.rsMembers, s.rsBarriers)
	h.nextID = s.nextID
	h.promotions = s.promotions
	h.youngGCs = s.youngGCs
	h.oldGCs = s.oldGCs
}

// validateRef 按固定拒绝次序校验一个引用参数：
// 未定义(从未分配的ID) > 悬垂(已回收)。
func (h *Heap) validateRefLocked(id uint64, field int, op string) error {
	if id == 0 || id >= h.nextID {
		return errf(ErrUndefined, op, id, field)
	}
	if _, dead := h.tomb[id]; dead {
		return errf(ErrDangling, op, id, field)
	}
	if _, ok := h.objects[id]; !ok {
		return errf(ErrDangling, op, id, field)
	}
	return nil
}

// Allocate 在年轻区分配 numFields 个引用槽的对象；空间不足时自动触发
// 一次年轻区回收，回收后仍不足则返回 ErrOutOfSpace 且不产生任何副作用
// （包括被自动触发的回收本身也回滚）。
func (h *Heap) Allocate(numFields int) (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if numFields < 0 {
		return 0, errf(ErrInvalidArgument, "allocate", 0, numFields)
	}
	if h.youngCountLocked() >= h.youngCap {
		snap := h.snapshotLocked()
		h.collectYoungLocked(true)
		if h.youngCountLocked() >= h.youngCap {
			h.restoreLocked(snap)
			return 0, errf(ErrOutOfSpace, "allocate", 0, numFields)
		}
	}
	id := h.nextID
	h.nextID++
	h.objects[id] = &Object{id: id, fields: make([]uint64, numFields)}
	return id, nil
}

// SetField 把 src.field 写为 dst（dst 为 0 表示清空）。字段写入与写屏障
// 登记在同一临界区内完成，因此对任何回收线程都不可分。
func (h *Heap) SetField(src uint64, field int, dst uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 拒绝次序：未定义 > 参数 > 悬垂 > 空间不足。
	if err := h.validateRefLocked(src, field, "set"); err != nil {
		return err
	}
	srcObj := h.objects[src]
	if field < 0 || field >= len(srcObj.fields) {
		return errf(ErrInvalidArgument, "set", src, field)
	}
	if dst != 0 {
		if err := h.validateRefLocked(dst, field, "set"); err != nil {
			return err
		}
	}
	srcObj.fields[field] = dst
	h.writeBarrier(src, dst)
	return nil
}

// GetField 读取 src.field。
func (h *Heap) GetField(src uint64, field int) (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.validateRefLocked(src, field, "get"); err != nil {
		return 0, err
	}
	srcObj := h.objects[src]
	if field < 0 || field >= len(srcObj.fields) {
		return 0, errf(ErrInvalidArgument, "get", src, field)
	}
	return srcObj.fields[field], nil
}

// AddRoot 把存活对象加入根集合。
func (h *Heap) AddRoot(id uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.validateRefLocked(id, 0, "add-root"); err != nil {
		return err
	}
	h.roots[id] = struct{}{}
	return nil
}

// RemoveRoot 把对象移出根集合；不存在的 ID 或未在根集合中的对象为空操作。
// 移除不立即回收，只在下一次回收时按可达性判定。
func (h *Heap) RemoveRoot(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.roots, id)
}

// IsRoot 报告 id 当前是否为根（观测用）。
func (h *Heap) IsRoot(id uint64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.roots[id]
	return ok
}

// CollectYoung 显式触发一次年轻区回收。
func (h *Heap) CollectYoung() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.collectYoungLocked(false)
}

// CollectOld 显式触发一次年老区回收；永不自动发生。
func (h *Heap) CollectOld() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.collectOldLocked()
}

// Stats 返回同一瞬间取样的一致性统计视图。
func (h *Heap) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Stats{
		YoungObjects:   h.youngCountLocked(),
		OldObjects:     h.oldCountLocked(),
		Promotions:     h.promotions,
		YoungGCs:       h.youngGCs,
		OldGCs:         h.oldGCs,
		RememberedSize: h.rs.size(),
		BarrierEntries: h.rs.barrierEntries,
	}
}

// reachableLocked 从根集合出发扫描整个托管空间，返回可达对象所在区。
// 仅用于观测/测试与年老区回收。
func (h *Heap) reachableLocked() map[uint64]Generation {
	live := make(map[uint64]Generation)
	stack := make([]uint64, 0, len(h.roots))
	for id := range h.roots {
		if o, ok := h.objects[id]; ok {
			if _, seen := live[id]; !seen {
				live[id] = o.gen
				stack = append(stack, id)
			}
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, ref := range h.objects[cur].fields {
			if ref == 0 {
				continue
			}
			o, ok := h.objects[ref]
			if !ok {
				continue
			}
			if _, seen := live[ref]; !seen {
				live[ref] = o.gen
				stack = append(stack, ref)
			}
		}
	}
	return live
}

// LiveSet 返回从根可达的全部存活对象及其所在区。
func (h *Heap) LiveSet() map[uint64]Generation {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reachableLocked()
}

// Object 返回对象的只读观测副本；已回收或从未分配返回 false。
func (h *Heap) Object(id uint64) (*Object, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	o, ok := h.objects[id]
	if !ok {
		return nil, false
	}
	return o.clone(), true
}
