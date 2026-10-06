package stackmgr

import "sync"

// frame 是协程栈中的一帧。baseOff 是其首个槽位在 co.stack 中的局部下标。
type frame struct {
	id      int64
	baseOff int
	slots   int
}

// coroutine 登记一个协程的栈、帧与统计。
type coroutine struct {
	mu        sync.Mutex
	id        int
	arenaBase int // 当前栈区在 arena 中的起始偏移
	stack     []Value
	size      int // 当前栈区尺寸（槽位）
	used      int // 已使用槽位
	frames    []frame
	frameIdx  map[int64]int // 帧 id -> 在 frames 中的下标，搬迁后仍有效
	maxSize   int
	growths   int
	shrinks   int
	nextFID   int64
	tracker   ptrTracker
}

func newCoroutine(id, size, arenaBase int) *coroutine {
	co := &coroutine{
		id:        id,
		arenaBase: arenaBase,
		stack:     make([]Value, size),
		size:      size,
		maxSize:   size,
		frameIdx:  make(map[int64]int),
		nextFID:   1, // 帧 id 从 1 开始且单调、永不复用
	}
	return co
}

// ptrTracker 只记录本栈中“实际存放了栈内指针”的槽位（局部下标）。
// 搬迁时遍历它即可完成全部修正，开销 O(P)，与栈总槽位数无关。
type ptrTracker struct {
	locs []int
	pos  map[int]int
}

func (t *ptrTracker) init() { t.pos = make(map[int]int) }

func (t *ptrTracker) add(locOff int) {
	if _, ok := t.pos[locOff]; ok {
		return
	}
	t.pos[locOff] = len(t.locs)
	t.locs = append(t.locs, locOff)
}

func (t *ptrTracker) drop(locOff int) {
	idx, ok := t.pos[locOff]
	if !ok {
		return
	}
	last := len(t.locs) - 1
	mov := t.locs[last]
	t.locs[idx] = mov
	t.pos[mov] = idx
	t.locs = t.locs[:last]
	delete(t.pos, locOff)
}

// dropRange 移除 [lo, hi) 内的登记项，用于弹帧；帧数固定为一帧槽位数量级。
func (t *ptrTracker) dropRange(lo, hi int) {
	for off := lo; off < hi; off++ {
		t.drop(off)
	}
}

// relocateTo 把 tracker 中登记的槽位搬到新栈切片，并给其中的每个指针
// 施加相同的地址平移 delta。仅访问真实指针槽，复杂度 O(P)。
func (t *ptrTracker) relocateTo(dst []Value, old []Value, delta int) {
	for _, loc := range t.locs {
		v := old[loc]
		if v.isPtr {
			v.ptr.arenaOff += delta
		}
		dst[loc] = v
	}
}

// count 返回本栈当前实际存在的栈内指针数。
func (t *ptrTracker) count() int { return len(t.locs) }

// finishRelocate 在已持有 co.mu 时完成拷贝、指针修正与可见状态切换。
func (co *coroutine) finishRelocate(base, newSize int) {
	next := make([]Value, newSize)
	copy(next, co.stack[:co.used])
	delta := base - co.arenaBase
	co.tracker.relocateTo(next, co.stack, delta)
	co.stack = next
	co.size = newSize
	co.arenaBase = base
}

// deref 固定拒绝次序地解析一次指针解引用：参数错误 → 悬垂 → 跨栈 → 未定义槽位。
func (co *coroutine) deref(coID int, p Pointer) (int, error) {
	if p.slot < 0 {
		return 0, errf(ClassParameter, "parameter: negative slot in pointer")
	}
	if p.coID != coID {
		return 0, errf(ClassCrossStack,
			"cross-stack pointer: pointer of coroutine %d dereferenced in coroutine %d", p.coID, coID)
	}
	idx, alive := co.frameIdx[p.frameID]
	if !alive {
		return 0, errf(ClassDangling, "dangling pointer: target frame %d has been popped", p.frameID)
	}
	fr := co.frames[idx]
	if p.slot >= fr.slots {
		return 0, errf(ClassUndefined, "undefined slot %d in target frame of %d slots", p.slot, fr.slots)
	}
	return fr.baseOff + p.slot, nil
}
