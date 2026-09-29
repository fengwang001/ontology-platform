package gencopy

import (
	"encoding/binary"
	"errors"
	"sort"
	"sync"
)

// 本包实现一个模拟堆上的分代复制式垃圾回收器。
//
// 模拟堆是一段连续的 []byte，按字节偏移划分四个区域：
//
//	+-------+---------+---------+-------+
//	| Eden  | Survivor| Survivor|  Old  |
//	|       |   A     |   B     |       |
//	+-------+---------+---------+-------+
//
// 两个幸存区等大，每次次要回收后互换 From/To 角色（复制式回收）。
// 每个对象的物理布局为：
//
//	对象头（24 字节） + numRefs 个 8 字节引用槽 + payload 字节
//
// 对外只暴露不透明的 Handle；对象在 Eden/幸存区/老年代之间移动时
// 句柄保持不变，由堆内的句柄表维护当前位置。

// Handle 是调用方访问对象的不透明句柄。0 保留为空引用 Nil。
type Handle uint64

// Nil 是空引用。
const Nil Handle = 0

// Stats 是累计回收统计。
type Stats struct {
	MinorCollections int
	Promotions       int
}

// Config 描述堆的容量参数（单位：字节）。
type Config struct {
	EdenSize     int // 年轻代分配区（Eden）大小
	SurvivorSize int // 每个幸存区大小（两个幸存区等大）
	OldSize      int // 老年代大小
	PromoteAge   int // 存活达到该次数（年龄）即晋升，必须 >= 1
}

// 可区分的拒绝原因。
var (
	// ErrInvalidHandle：句柄从未由本堆颁发。
	ErrInvalidHandle = errors.New("gencopy: invalid handle")
	// ErrHandleReclaimed：句柄对应对象已被回收。
	ErrHandleReclaimed = errors.New("gencopy: handle has been reclaimed")
	// ErrSlotOutOfRange：引用字段下标越界。
	ErrSlotOutOfRange = errors.New("gencopy: reference slot index out of range")
	// ErrPayloadTooLarge：载荷超过 Eden 大小，任何情况下都无法分配。
	ErrPayloadTooLarge = errors.New("gencopy: payload larger than eden")
	// ErrObjectTooLarge：载荷虽不超限，但含对象头的完整对象超过 Eden 大小。
	ErrObjectTooLarge = errors.New("gencopy: object larger than eden")
	// ErrOldFull：老年代放不下待晋升对象，本次次要回收整体撤回。
	ErrOldFull = errors.New("gencopy: old generation cannot hold promoted object")
	// ErrBadConfig：构造参数非法。
	ErrBadConfig = errors.New("gencopy: invalid configuration")
)

const (
	hdrAge        = 0  // uint32：存活次数（Eden 新对象为 0）
	hdrNumRefs    = 4  // uint32
	hdrPayloadLen = 8  // uint32
	hdrFwdGen     = 12 // uint8：0=无转发地址，1=转发到幸存区，2=转发到老年代
	hdrFwdAddr    = 16 // uint64：转发目标的绝对偏移
	headerSize    = 24
)

const (
	genNone  = 0
	genYoung = 1
	genOld   = 2
)

const (
	regionEden = iota
	regionSurvA
	regionSurvB
	regionOld
)

// region 描述堆内一段连续区域。
type region struct {
	name  string
	start int
	size  int
	used  int
}

func (r *region) end() int { return r.start + r.size }

// loc 是对象当前所在位置：区域 + 区域内偏移。
type loc struct {
	region int
	off    int
}

// Heap 是分代复制 GC 的模拟堆。所有方法均可被并发调用。
type Heap struct {
	mu sync.Mutex

	mem []byte // 整堆模拟内存

	eden  region
	survA region
	survB region
	old   region

	from *region // 当前存活对象所在的幸存区
	to   *region // 下一次次要回收使用的空闲幸存区

	nextID uint64

	objs       map[Handle]*loc // 存活对象句柄 -> 位置
	byAddr     map[int]Handle  // 绝对偏移 -> 句柄
	tombstones map[Handle]struct{}

	roots []Handle // 根集，有序、去重，保证操作序列结果确定

	// 记忆集（remembered set）：字段中含有年轻代引用的老年对象句柄。
	// 维护为精确集合：写屏障加入；重写后不再含年轻引用时立即移出；
	// 每次次要回收后再次精确重建。
	rs []Handle

	promoteAge   int
	minorCount   int
	promoteCount int
}

// NewHeap 创建一个模拟堆。
func NewHeap(cfg Config) (*Heap, error) {
	if cfg.EdenSize <= 0 || cfg.SurvivorSize <= 0 || cfg.OldSize <= 0 || cfg.PromoteAge < 1 {
		return nil, ErrBadConfig
	}
	total := cfg.EdenSize + 2*cfg.SurvivorSize + cfg.OldSize
	off := 0
	h := &Heap{
		mem:        make([]byte, total),
		objs:       make(map[Handle]*loc),
		byAddr:     make(map[int]Handle),
		tombstones: make(map[Handle]struct{}),
		promoteAge: cfg.PromoteAge,
	}
	h.eden = region{name: "eden", start: off, size: cfg.EdenSize}
	off += cfg.EdenSize
	h.survA = region{name: "survivor-A", start: off, size: cfg.SurvivorSize}
	off += cfg.SurvivorSize
	h.survB = region{name: "survivor-B", start: off, size: cfg.SurvivorSize}
	off += cfg.SurvivorSize
	h.old = region{name: "old", start: off, size: cfg.OldSize}
	h.from = &h.survA
	h.to = &h.survB
	return h, nil
}

func objectSize(numRefs, payloadLen int) int {
	return headerSize + 8*numRefs + payloadLen
}

func (h *Heap) abs(l *loc) int {
	return h.regionOf(l.region).start + l.off
}

func (h *Heap) regionOf(id int) *region {
	switch id {
	case regionEden:
		return &h.eden
	case regionSurvA:
		return &h.survA
	case regionSurvB:
		return &h.survB
	default:
		return &h.old
	}
}

func (h *Heap) isYoung(l *loc) bool {
	return l.region != regionOld
}

func putU64(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }
func getU64(b []byte) uint64    { return binary.LittleEndian.Uint64(b) }

const nilAddr = ^uint64(0)

// resolve 必须在持锁状态下调用。
func (h *Heap) resolve(obj Handle) (*loc, error) {
	if obj == Nil {
		return nil, ErrInvalidHandle
	}
	if l, ok := h.objs[obj]; ok {
		return l, nil
	}
	if _, dead := h.tombstones[obj]; dead {
		return nil, ErrHandleReclaimed
	}
	return nil, ErrInvalidHandle
}

// Allocate 在年轻代分配对象，Eden 满时先触发一次次要回收；
// 回收后仍放不下则以 ErrObjectTooLarge 拒绝。
func (h *Heap) Allocate(numRefs int, payload []byte) (Handle, error) {
	if numRefs < 0 {
		return Nil, ErrBadConfig
	}
	payloadLen := len(payload)
	if payloadLen > h.eden.size {
		return Nil, ErrPayloadTooLarge
	}
	size := objectSize(numRefs, payloadLen)
	if size > h.eden.size {
		return Nil, ErrObjectTooLarge
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.eden.used+size > h.eden.size {
		// 分配区满：先做一次次要回收（Cheney 复制 + 晋升）。
		if err := h.minorGC(); err != nil {
			return Nil, err
		}
		if h.eden.used+size > h.eden.size {
			return Nil, ErrObjectTooLarge
		}
	}

	h.nextID++
	hdl := Handle(h.nextID)
	off := h.eden.start + h.eden.used
	h.eden.used += size

	hdr := h.mem[off : off+headerSize]
	binary.LittleEndian.PutUint32(hdr[hdrAge:], 0)
	binary.LittleEndian.PutUint32(hdr[hdrNumRefs:], uint32(numRefs))
	binary.LittleEndian.PutUint32(hdr[hdrPayloadLen:], uint32(payloadLen))
	hdr[hdrFwdGen] = genNone
	putU64(hdr[hdrFwdAddr:], nilAddr)
	body := h.mem[off+headerSize:]
	for i := 0; i < numRefs; i++ {
		putU64(body[8*i:8*i+8], nilAddr)
	}
	if payloadLen > 0 {
		copy(body[8*numRefs:8*numRefs+payloadLen], payload)
	}

	l := &loc{region: regionEden, off: off - h.eden.start}
	h.objs[hdl] = l
	h.byAddr[off] = hdl
	return hdl, nil
}

func (h *Heap) numRefsAt(addr int) int {
	return int(binary.LittleEndian.Uint32(h.mem[addr+hdrNumRefs:]))
}

func (h *Heap) numRefs(l *loc) int {
	return h.numRefsAt(h.abs(l))
}

// GetRef 读取对象的引用字段。
func (h *Heap) GetRef(obj Handle, slot int) (Handle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, err := h.resolve(obj)
	if err != nil {
		return Nil, err
	}
	if slot < 0 || slot >= h.numRefs(l) {
		return Nil, ErrSlotOutOfRange
	}
	addr := h.abs(l)
	val := getU64(h.mem[addr+headerSize+8*slot:])
	if val == nilAddr {
		return Nil, nil
	}
	return h.byAddr[int(val)], nil
}

// SetRef 写入对象的引用字段。老年对象写入年轻代引用时触发写屏障，
// 把老年对象记入记忆集；若重写后该老年对象不再含任何年轻代引用，
// 则把它从记忆集移出。
func (h *Heap) SetRef(obj Handle, slot int, ref Handle) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, err := h.resolve(obj)
	if err != nil {
		return err
	}
	if slot < 0 || slot >= h.numRefs(l) {
		return ErrSlotOutOfRange
	}
	var refAddr uint64 = nilAddr
	if ref != Nil {
		refLoc, rerr := h.resolve(ref)
		if rerr != nil {
			return rerr
		}
		refAddr = uint64(h.abs(refLoc))
	}
	addr := h.abs(l)
	putU64(h.mem[addr+headerSize+8*slot:], refAddr)

	// 写屏障：仅 老年代 -> 年轻代 引用需要记忆集维护。
	if l.region == regionOld {
		h.refreshOldRemembered(obj, l)
	}
	return nil
}

// refreshOldRemembered 精确判定老年对象是否仍含年轻代引用，
// 据此加入或移出记忆集。必须持锁调用。
func (h *Heap) refreshOldRemembered(obj Handle, l *loc) {
	addr := h.abs(l)
	n := h.numRefsAt(addr)
	containsYoung := false
	for i := 0; i < n; i++ {
		v := getU64(h.mem[addr+headerSize+8*i:])
		if v != nilAddr && v < uint64(h.old.start) {
			containsYoung = true
			break
		}
	}
	idx := sort.Search(len(h.rs), func(i int) bool { return h.rs[i] >= obj })
	present := idx < len(h.rs) && h.rs[idx] == obj
	switch {
	case containsYoung && !present:
		h.rs = append(h.rs, Nil)
		copy(h.rs[idx+1:], h.rs[idx:])
		h.rs[idx] = obj
	case !containsYoung && present:
		h.rs = append(h.rs[:idx], h.rs[idx+1:]...)
	}
}

// Payload 返回对象载荷的拷贝。
func (h *Heap) Payload(obj Handle) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, err := h.resolve(obj)
	if err != nil {
		return nil, err
	}
	addr := h.abs(l)
	plen := int(binary.LittleEndian.Uint32(h.mem[addr+hdrPayloadLen:]))
	out := make([]byte, plen)
	copy(out, h.mem[addr+headerSize+8*h.numRefsAt(addr):addr+headerSize+8*h.numRefsAt(addr)+plen])
	return out, nil
}

// AddRoot 显式登记一个根。重复登记幂等。
func (h *Heap) AddRoot(obj Handle) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.resolve(obj); err != nil {
		return err
	}
	idx := sort.Search(len(h.roots), func(i int) bool { return h.roots[i] >= obj })
	if idx < len(h.roots) && h.roots[idx] == obj {
		return nil
	}
	h.roots = append(h.roots, Nil)
	copy(h.roots[idx+1:], h.roots[idx:])
	h.roots[idx] = obj
	return nil
}

// RemoveRoot 注销一个根。注销不存在的根是幂等的；
// 已被回收的句柄也会被顺带清理。
func (h *Heap) RemoveRoot(obj Handle) {
	h.mu.Lock()
	defer h.mu.Unlock()
	idx := sort.Search(len(h.roots), func(i int) bool { return h.roots[i] >= obj })
	if idx < len(h.roots) && h.roots[idx] == obj {
		h.roots = append(h.roots[:idx], h.roots[idx+1:]...)
	}
}

// ForceMinorGC 强制执行一次次要回收。
func (h *Heap) ForceMinorGC() (Stats, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := Stats{MinorCollections: h.minorCount, Promotions: h.promoteCount}
	if err := h.minorGC(); err != nil {
		return Stats{}, err
	}
	return Stats{MinorCollections: h.minorCount, Promotions: h.promoteCount}.sub(before), nil
}

// Stats 返回累计回收/晋升次数。
func (h *Heap) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Stats{MinorCollections: h.minorCount, Promotions: h.promoteCount}
}

func (s Stats) sub(o Stats) Stats {
	return Stats{MinorCollections: s.MinorCollections - o.MinorCollections, Promotions: s.Promotions - o.Promotions}
}

// UsedBytes 返回年轻代与老年代当前已用字节数，
// 严格等于各代内存活对象字节之和。
func (h *Heap) UsedBytes() (young, old int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.eden.used + h.from.used, h.old.used
}
