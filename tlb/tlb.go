package tlb

import (
	"errors"
	"math"
	"sync"
)

// 错误集合。所有被拒绝的操作保证不改变任何状态。
var (
	ErrBadConfig = errors.New("tlb: invalid configuration")
	ErrBadCPU    = errors.New("tlb: cpu index out of range")
	ErrBadMM     = errors.New("tlb: mm not created")
	ErrDead      = errors.New("tlb: mm already destroyed")
	ErrBusy      = errors.New("tlb: mm still in use by a cpu")
	ErrTooManyMM = errors.New("tlb: too many mms")
	ErrNoContext = errors.New("tlb: cpu has no active context")
	ErrBadParam  = errors.New("tlb: parameter out of range")
)

// Pair 是一个 (ASID, 世代) 对。ASID 为 0 表示"无"。
type Pair struct {
	ASID uint32
	Gen  uint64
}

// Key 是 TLB 条目的键 (asid, vpn)。
type Key struct {
	ASID uint32
	VPN  uint32
}

// Entry 是一条 TLB 条目。
type Entry struct {
	Key Key
	PFN uint32
}

// Config 是模型构造参数。
type Config struct {
	ASIDs  int // A：ASID 个数，2..4096，可用编号 1..A
	CPUs   int // C：CPU 数，1..16
	TLBCap int // T：每 CPU 的 TLB 容量，1..64
	MaxMMs int // M：地址空间（mm）数上限，1..1e5
}

func (c Config) valid() bool {
	return c.ASIDs >= 2 && c.ASIDs <= 4096 &&
		c.CPUs >= 1 && c.CPUs <= 16 &&
		c.TLBCap >= 1 && c.TLBCap <= 64 &&
		c.MaxMMs >= 1 && c.MaxMMs <= 100000 &&
		c.ASIDs > c.CPUs
}

type mmState struct {
	asid  uint32
	gen   uint64
	alive bool
}

// Model 是多 CPU 软件 TLB 模型。所有方法可并发调用，
// 内部以单一互斥锁串行化，回绕的各步对外表现为一个原子步骤。
type Model struct {
	mu    sync.Mutex
	cfg   Config
	gen   uint64 // 全局世代 G，初值 1，只增不减
	alloc asidAlloc
	cpus  []cpuState
	mms   []mmState

	// 复杂度计数器（非导出）：mmVisited 统计对 mm 表的遍历次数，
	// 任何操作都不得遍历 mm 表，因此它必须恒为 0。
	mmVisited uint64
	// invalidateProbes 统计 Invalidate 的条目探测次数，每次调用不超过 C。
	invalidateProbes uint64
}

// New 按配置构造模型；配置非法时整体拒绝并返回 ErrBadConfig。
func New(cfg Config) (*Model, error) {
	if !cfg.valid() {
		return nil, ErrBadConfig
	}
	m := &Model{
		cfg:   cfg,
		gen:   1,
		alloc: newAsidAlloc(cfg.ASIDs),
		cpus:  make([]cpuState, cfg.CPUs),
	}
	for i := range m.cpus {
		m.cpus[i].tlb = newCPUTLB(cfg.TLBCap)
	}
	return m, nil
}

// CreateMM 创建地址空间，依次返回编号 0、1、2…，初值 (asid, gen) = (0, 0)。
// 超过 M 个报 ErrTooManyMM。
func (m *Model) CreateMM() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.mms) >= m.cfg.MaxMMs {
		return 0, ErrTooManyMM
	}
	m.mms = append(m.mms, mmState{alive: true})
	return len(m.mms) - 1, nil
}

// Switch 将 cpu 切换到 mm，返回 (asid, gen, flushed, rolled)。
func (m *Model) Switch(cpu, mm int) (asid uint32, gen uint64, flushed, rolled bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cpu < 0 || cpu >= m.cfg.CPUs {
		return 0, 0, false, false, ErrBadCPU
	}
	if mm < 0 || mm >= len(m.mms) {
		return 0, 0, false, false, ErrBadMM
	}
	e := &m.mms[mm]
	if !e.alive {
		return 0, 0, false, false, ErrDead
	}
	// 第一步：确定 mm 的 (asid, gen)，至多回绕一次。
	for attempt := 0; attempt < 2; attempt++ {
		if e.gen == m.gen {
			break // 快速路径：沿用
		}
		if e.asid != 0 && m.reservedHas(e.asid, e.gen) {
			e.gen = m.gen // 提升：保留 asid，不改 taken
			break
		}
		if a := m.alloc.alloc(); a != 0 {
			e.asid, e.gen = a, m.gen
			break
		}
		m.rollover()
		rolled = true
	}
	if e.gen != m.gen || e.asid == 0 {
		panic("tlb: switch failed to assign context")
	}
	// 第二步：惰性整表刷新。
	c := &m.cpus[cpu]
	if c.pending {
		c.tlb.clear()
		c.pending = false
		flushed = true
	}
	// 第三步：更新 active。
	c.active = Pair{ASID: e.asid, Gen: e.gen}
	return e.asid, e.gen, flushed, rolled, nil
}

// rollover 执行 ASID 回绕：G 加一，reserved 置为各 CPU 当时的 active，
// taken 重置为 reserved 的 asid 集合，所有 CPU 的 pending 置真。
// 工作量为 O(C + A/64)，不遍历 mm 表。
func (m *Model) rollover() {
	m.gen++
	for i := range m.cpus {
		m.cpus[i].reserved = m.cpus[i].active
		m.cpus[i].pending = true
	}
	m.alloc.reset()
	for i := range m.cpus {
		if r := m.cpus[i].reserved; r.ASID != 0 {
			m.alloc.set(r.ASID)
		}
	}
}

// reservedHas 报告 (asid, gen) 是否恰等于某个 CPU 的 reserved 对。O(C)。
func (m *Model) reservedHas(asid uint32, gen uint64) bool {
	for i := range m.cpus {
		if r := m.cpus[i].reserved; r.ASID == asid && r.Gen == gen {
			return true
		}
	}
	return false
}

// Fill 以 cpu 当前 active 的 asid 为标签插入或更新 (asid, vpn)->pfn，
// 并置为最近使用。超过容量时淘汰最久未用者并返回其键。
func (m *Model) Fill(cpu int, vpn, pfn uint64) (evicted Key, hasEvicted bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cpu < 0 || cpu >= m.cfg.CPUs {
		return Key{}, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 || pfn > math.MaxUint32 {
		return Key{}, false, ErrBadParam
	}
	c := &m.cpus[cpu]
	if c.active.ASID == 0 {
		return Key{}, false, ErrNoContext
	}
	return c.tlb.fill(Key{ASID: c.active.ASID, VPN: uint32(vpn)}, uint32(pfn))
}

// Lookup 以 cpu 当前 active 的 asid 查找 vpn，命中返回 pfn 并置为最近使用，
// 未命中不改任何状态。
func (m *Model) Lookup(cpu int, vpn uint64) (pfn uint32, hit bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cpu < 0 || cpu >= m.cfg.CPUs {
		return 0, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 {
		return 0, false, ErrBadParam
	}
	c := &m.cpus[cpu]
	if c.active.ASID == 0 {
		return 0, false, ErrNoContext
	}
	return c.tlb.lookup(Key{ASID: c.active.ASID, VPN: uint32(vpn)})
}

// Invalidate 在 mm 的上下文有效时，从所有 CPU 删除键 (mm.asid, vpn)，
// 返回删除了条目的 CPU 个数；上下文失效时返回 0 且不改任何状态。
func (m *Model) Invalidate(mm int, vpn uint64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mm < 0 || mm >= len(m.mms) {
		return 0, ErrBadMM
	}
	e := &m.mms[mm]
	if !e.alive {
		return 0, ErrDead
	}
	if vpn > math.MaxUint32 {
		return 0, ErrBadParam
	}
	if e.asid == 0 || (e.gen != m.gen && !m.reservedHas(e.asid, e.gen)) {
		return 0, nil
	}
	removed := 0
	key := Key{ASID: e.asid, VPN: uint32(vpn)}
	for i := range m.cpus {
		m.invalidateProbes++
		if m.cpus[i].tlb.invalidate(key) {
			removed++
		}
	}
	return removed, nil
}

// DestroyMM 销毁 mm（编号不回收）。返回 (是否立即释放了 asid, 删除的 TLB 条目数)。
func (m *Model) DestroyMM(mm int) (released bool, removed int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mm < 0 || mm >= len(m.mms) {
		return false, 0, ErrBadMM
	}
	e := &m.mms[mm]
	if !e.alive {
		return false, 0, ErrDead
	}
	if e.asid != 0 && e.gen == m.gen {
		for i := range m.cpus {
			if a := m.cpus[i].active; a.ASID == e.asid && a.Gen == e.gen {
				return false, 0, ErrBusy
			}
		}
	}
	e.alive = false
	if e.asid != 0 && e.gen == m.gen && !m.reservedASID(e.asid) {
		m.alloc.free(e.asid)
		for i := range m.cpus {
			removed += m.cpus[i].tlb.purgeASID(e.asid)
		}
		return true, removed, nil
	}
	return false, 0, nil
}

// reservedASID 报告 asid 是否等于某个 CPU 的 reserved 对的 asid。O(C)。
func (m *Model) reservedASID(asid uint32) bool {
	for i := range m.cpus {
		if m.cpus[i].reserved.ASID == asid {
			return true
		}
	}
	return false
}

// ---- 只读状态访问器（用于测试与复现）----

// Gen 返回当前全局世代 G。
func (m *Model) Gen() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen
}

// MM 返回编号为 mm 的地址空间的 (asid, gen, alive)。
func (m *Model) MM(mm int) (asid uint32, gen uint64, alive bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.mms[mm]
	return e.asid, e.gen, e.alive
}

// CPUState 返回 cpu 的 active、reserved 与 pending。
func (m *Model) CPUState(cpu int) (active, reserved Pair, pending bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cpus[cpu]
	return c.active, c.reserved, c.pending
}

// TLBEntries 按最近使用在前返回 cpu 的 TLB 内容。
func (m *Model) TLBEntries(cpu int) []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cpus[cpu].tlb.entries()
}

// Taken 报告 asid 是否在已占用集合中。
func (m *Model) Taken(asid uint32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alloc.taken(asid)
}
