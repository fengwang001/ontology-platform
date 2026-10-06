package stackmgr

// Package stackmgr 实现语言运行时的可增长协程栈管理子系统。
//
// 核心能力
//   - 每个协程初始仅占 BaseSize；压帧空间不足时整块搬迁到更大的
//     base·F^k 尺寸，并修正全部栈内指针；长期低水位时自动收缩。
//   - 增长受单协程上限与全局总配额约束；搬迁失败旧栈完好。
//   - 栈内指针按帧唯一 id 判悬垂；跨栈写入与栈外逃逸在写入时即拒绝。
//   - 提供同瞬间的统计视图；并发下配额精确、可线性化。
//
// 典型用法
//
//	m, err := stackmgr.New(stackmgr.Config{
//	    BaseSize: 4, GrowthFactor: 2, MaxStackSize: 64,
//	    TotalQuota: 4096, ShrinkRatio: stackmgr.ShrinkRatio{Num: 1, Den: 4},
//	})
//	co, _ := m.Spawn()
//	_ = m.Push(co, 8)
//	p, _ := m.AddrOf(co, -1, 0)
//	_ = m.WriteIntAt(co, p, 42)
//
// 错误均为 *stackmgr.StackError，可用其 Class 字段按固定次序判定。
// 设计取舍见同目录 DESIGN.md。

import "sync"

// Stats 是某一瞬间的全局统计视图：所有协程数据与配额来自同一快照。
type Stats struct {
	Coroutines []CoStat
	QuotaUsed  int
	QuotaTotal int
}

// CoStat 是单个协程的统计。
type CoStat struct {
	ID      int
	Size    int // 当前栈尺寸
	Used    int // 当前已用槽位
	MaxSize int // 历史最大尺寸
	Growths int // 累计增长次数
	Shrinks int // 累计收缩次数
}

// QuotaRemaining 返回全局配额剩余。
func (s Stats) QuotaRemaining() int { return s.QuotaTotal - s.QuotaUsed }

// Manager 是可增长协程栈管理子系统。
//
// 锁序（全局唯一，杜绝死锁）：Manager.mu → coroutine.mu → quotaAccountant.mu。
// 指针、帧的全部校验都在已持有 coroutine.mu 时进行。
type Manager struct {
	mu     sync.Mutex // 协程表
	snapMu sync.Mutex // 搬迁原子段与统计快照互斥；配额/arena 仅在其下访问
	cfg    Config
	qa     *quotaAccountant
	cos    map[int]*coroutine
	nextID int
	extern map[int]Value // 栈外全局表：登记栈内指针到此处即逃逸
}

// New 依据配置创建子系统；配置不合法以 ClassConfig 拒绝整个实例。
func New(cfg Config) (*Manager, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Manager{
		cfg:    cfg,
		qa:     newQuotaAccountant(cfg.TotalQuota),
		cos:    make(map[int]*coroutine),
		extern: make(map[int]Value),
	}, nil
}

// FailNextAlloc 使接下来 n 次内存申请失败（测试钩子）。
func (m *Manager) FailNextAlloc(n int) {
	m.snapMu.Lock()
	m.qa.failN = n
	m.snapMu.Unlock()
}

// resolveCo 处理“未定义协程”（最高优先级）并返回加锁后的协程。
func (m *Manager) resolveCo(coID int) (*coroutine, error) {
	m.mu.Lock()
	co, ok := m.cos[coID]
	m.mu.Unlock()
	if !ok {
		return nil, errf(ClassUndefined, "undefined coroutine %d", coID)
	}
	co.mu.Lock()
	return co, nil
}

// Spawn 创建协程：立即分配 BaseSize 栈区并占用配额；
// 预留与申请在 qa.mu 同一临界区内原子完成，
// 配额不足或申请被拒以 ClassQuota 拒绝，且不留任何痕迹。
func (m *Manager) Spawn() (int, error) {
	// 整个“扣配额 → 申请 → 登记”在 Manager.mu 内原子完成，
	// 使任何快照要么看不到该协程（也未扣配额），要么两者同时可见。
	// 此处不再取 qa.mu：Manager.mu 是更外层锁，直接调用其 Locked 版本即可。
	// 配额/申请在 snapMu 下原子；随后在 m.mu 下登记。
	// 扣配额、申请与登记都在 snapMu 内原子完成（其中短暂取 m.mu，
	// 锁序 snapMu → Manager.mu 与 Stats 同向，无环）。
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	if !m.qa.tryReserveLocked(m.cfg.BaseSize) {
		return 0, errf(ClassQuota, "quota: cannot spawn, base size %d unavailable", m.cfg.BaseSize)
	}
	base, ok := m.qa.allocLocked(m.cfg.BaseSize)
	if !ok {
		m.qa.releaseLocked(m.cfg.BaseSize)
		return 0, errf(ClassQuota, "quota: initial allocation rejected")
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	co := newCoroutine(id, m.cfg.BaseSize, base)
	co.tracker.init()
	m.cos[id] = co
	m.mu.Unlock()
	return id, nil
}

// slotRef 在已锁协程内定位帧槽；fi 可为相对下标（负数）或绝对下标。
func (co *coroutine) slotRef(fi, slot int) (int, error) {
	n := len(co.frames)
	if fi < 0 {
		fi += n
	}
	if fi < 0 || fi >= n {
		return 0, errf(ClassUndefined, "undefined frame index %d (have %d frames)", fi, n)
	}
	fr := co.frames[fi]
	if slot < 0 || slot >= fr.slots {
		return 0, errf(ClassUndefined, "undefined slot %d in frame of %d slots", slot, fr.slots)
	}
	return fr.baseOff + slot, nil
}

// Push 压入固定槽位的帧；空间不足时先增长（受上限与总配额约束）。
func (m *Manager) Push(coID, frameSlots int) error {
	co, err := m.resolveCo(coID)
	if err != nil {
		return err
	}
	defer co.mu.Unlock()
	if frameSlots <= 0 {
		return errf(ClassParameter, "parameter: frameSlots must be positive, got %d", frameSlots)
	}
	need := co.used + frameSlots
	if need > m.cfg.MaxStackSize {
		// 拒绝次序：栈溢出优先于配额不足；两者同时成立仍报栈溢出。
		return errf(ClassStackOverflow, "stack overflow: need %d slots over per-coroutine max %d",
			need, m.cfg.MaxStackSize)
	}
	// 不触发搬迁时压帧是常量开销；触发时 grow 原子处理配额与申请，
	// 失败不留任何增长痕迹，原栈完好。
	if need > co.size {
		grew, gerr := m.grow(co, need)
		if gerr != nil {
			return gerr
		}
		if grew {
			co.growths++
		}
	}
	// 配额已在 relocate 成功时计入，这里只登记帧；新槽零值不是指针。
	fid := co.nextFID
	co.nextFID++
	co.frames = append(co.frames, frame{id: fid, baseOff: co.used, slots: frameSlots})
	co.frameIdx[fid] = len(co.frames) - 1
	co.used = need
	if co.size > co.maxSize {
		co.maxSize = co.size
	}
	return nil
}

// Pop 弹出顶帧；被弹出帧上的指针即刻悬垂，随后按阈值自动收缩。
func (m *Manager) Pop(coID int) error {
	co, err := m.resolveCo(coID)
	if err != nil {
		return err
	}
	defer co.mu.Unlock()
	n := len(co.frames)
	if n == 0 {
		return errf(ClassParameter, "parameter: cannot pop empty stack")
	}
	top := co.frames[n-1]
	// 指向被弹出帧的指针立刻悬垂：移除其登记并摘除帧。
	co.tracker.dropRange(top.baseOff, top.baseOff+top.slots)
	co.used = top.baseOff
	delete(co.frameIdx, top.id)
	co.frames = co.frames[:n-1]
	// 清空已弹槽，防止通过旧偏移误读（悬垂以 frameID 判定为主）。
	for i := top.baseOff; i < top.baseOff+top.slots; i++ {
		co.stack[i] = Value{}
	}
	// 自动收缩：阈值严格小于增长后使用率下限，故不会抖动。
	if co.size > m.cfg.BaseSize && m.cfg.shouldShrink(co.used, co.size) {
		target := shrinkSize(co.size, co.used, m.cfg.BaseSize, m.cfg.GrowthFactor)
		if target < co.size && m.shrink(co, target) {
			co.shrinks++
		}
	}
	return nil
}

// FrameCount 返回当前帧数。
func (m *Manager) FrameCount(coID int) (int, error) {
	co, err := m.resolveCo(coID)
	if err != nil {
		return 0, err
	}
	defer co.mu.Unlock()
	return len(co.frames), nil
}

// AddrOf 取某协程栈内某帧某槽位的栈内指针。
func (m *Manager) AddrOf(coID, fi, slot int) (Pointer, error) {
	co, err := m.resolveCo(coID)
	if err != nil {
		return Pointer{}, err
	}
	defer co.mu.Unlock()
	off, err := co.slotRef(fi, slot)
	if err != nil {
		return Pointer{}, err
	}
	fid := co.frames[fiIndex(co, fi)].id
	return Pointer{coID: coID, frameID: fid, slot: slot, arenaOff: co.arenaBase + off}, nil
}

func fiIndex(co *coroutine, fi int) int {
	if fi < 0 {
		return fi + len(co.frames)
	}
	return fi
}

// WriteInt 向槽位写入普通值。
func (m *Manager) WriteInt(coID, fi, slot int, v int64) error {
	co, err := m.resolveCo(coID)
	if err != nil {
		return err
	}
	defer co.mu.Unlock()
	off, err := co.slotRef(fi, slot)
	if err != nil {
		return err
	}
	co.stack[off] = IntValue(v)
	co.tracker.drop(off)
	return nil
}

// WritePtr 把栈内指针写入槽位：同栈允许，跨栈报 ClassCrossStack。
// 若参数 p 本身悬垂，按拒绝次序先报 ClassDangling。
func (m *Manager) WritePtr(coID int, fi, slot int, p Pointer) error {
	co, err := m.resolveCo(coID)
	if err != nil {
		return err
	}
	defer co.mu.Unlock()
	off, err := co.slotRef(fi, slot)
	if err != nil {
		return err
	}
	if p.slot < 0 {
		return errf(ClassParameter, "parameter: negative slot in pointer")
	}
	// 跨栈指针在写入时即拒绝（不依赖其目标是否存活）；同协程已死帧才是悬垂。
	if p.coID != coID {
		return errf(ClassCrossStack,
			"cross-stack pointer: pointer of coroutine %d written into coroutine %d", p.coID, coID)
	}
	if _, ok := co.frameIdx[p.frameID]; !ok {
		return errf(ClassDangling, "dangling pointer: cannot store pointer to popped frame %d", p.frameID)
	}
	co.stack[off] = PtrValue(p)
	co.tracker.add(off)
	return nil
}

// ReadInt 经由栈内指针读取普通值；悬垂/类型不符分别报错。
func (m *Manager) ReadInt(coID int, p Pointer) (int64, error) {
	co, err := m.resolveCo(coID)
	if err != nil {
		return 0, err
	}
	defer co.mu.Unlock()
	off, err := co.deref(coID, p)
	if err != nil {
		return 0, err
	}
	v := co.stack[off]
	if iv, ok := v.Int(); ok {
		return iv, nil
	}
	return 0, errf(ClassParameter, "parameter: target slot holds a pointer, not an int")
}

// WriteIntAt 经由栈内指针写入新普通值。
func (m *Manager) WriteIntAt(coID int, p Pointer, v int64) error {
	co, err := m.resolveCo(coID)
	if err != nil {
		return err
	}
	defer co.mu.Unlock()
	off, err := co.deref(coID, p)
	if err != nil {
		return err
	}
	co.stack[off] = IntValue(v)
	co.tracker.drop(off)
	return nil
}

// PublishInt 把普通值登记到栈外全局表（不涉及逃逸）。
func (m *Manager) PublishInt(key int, v int64) {
	m.mu.Lock()
	m.extern[key] = IntValue(v)
	m.mu.Unlock()
}

// PublishPtr 试图把栈内指针逃逸到栈外全局表，立即以 ClassEscape 拒绝，
// 且不做任何登记。
func (m *Manager) PublishPtr(coID int, p Pointer, key int) error {
	if _, err := m.resolveCo(coID); err != nil {
		return err
	}
	// resolveCo 已加锁，立即释放：逃逸判定不需要持锁写状态。
	m.cos[coID].mu.Unlock()
	return errf(ClassEscape, "escape: stack-internal pointer to coroutine %d must not leave the stack", coID)
}

// ExternalInt 读取已登记到栈外的普通值（统计/测试辅助）。
func (m *Manager) ExternalInt(key int) (int64, bool) {
	m.mu.Lock()
	v, ok := m.extern[key]
	m.mu.Unlock()
	if !ok {
		return 0, false
	}
	iv, isInt := v.Int()
	return iv, isInt
}

// Stats 输出同一瞬间的统计视图。
//
// 先取 snapMu（所有配额/arena 访问与搬迁原子段都在其下），
// 再按 id 升序取齐全部 coroutine.mu。持有 snapMu 期间不可能有搬迁
// 在途（grow 用 TryLock，抢不到会先释放 co.mu 退避），因而
// 尺寸集合与配额占用必然一致，且 sum(尺寸)==配额占用。
func (m *Manager) Stats() Stats {
	m.snapMu.Lock()
	m.mu.Lock()
	ids := make([]int, 0, len(m.cos))
	for id := range m.cos {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	refs := make([]*coroutine, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, m.cos[id])
	}
	m.mu.Unlock()
	for _, co := range refs {
		co.mu.Lock()
	}
	st := Stats{QuotaUsed: m.qa.used, QuotaTotal: m.qa.total}
	for _, co := range refs {
		st.Coroutines = append(st.Coroutines, CoStat{
			ID: co.id, Size: co.size, Used: co.used,
			MaxSize: co.maxSize, Growths: co.growths, Shrinks: co.shrinks,
		})
	}
	for _, co := range refs {
		co.mu.Unlock()
	}
	m.snapMu.Unlock()
	return st
}
