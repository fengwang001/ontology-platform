// Package gengc 实现一个模拟堆上的分代复制式垃圾回收器。
//
// 堆布局：年轻代 = Eden（分配区）+ 两个等大幸存区 From/To；
// 老年代 Old 存放晋升对象，每次回收顺便在老年代内部整理。
// 回收期间调用方只能看到回收前或回收完成后的状态（互斥锁保证原子性）。
package gengc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// 可区分的拒绝原因，调用方可用 errors.Is 精确判定。
var (
	// ErrHandleReclaimed：访问的句柄已不可达并被回收。
	ErrHandleReclaimed = errors.New("gengc: 句柄指向的对象已被回收")
	// ErrFieldIndex：引用字段下标越界。
	ErrFieldIndex = errors.New("gengc: 引用字段下标越界")
	// ErrPayloadTooLarge：对象（含头部）超过分配区大小。
	ErrPayloadTooLarge = errors.New("gengc: 对象（含头部）超过分配区大小")
	// ErrOldGenFull：老年代放不下晋升对象；出现时本次回收整体撤回。
	ErrOldGenFull = errors.New("gengc: 老年代剩余空间不足以容纳晋升对象")
)

const (
	headerSize = 14 // 4(handleID)+2(age)+4(nfields)+4(payloadLen)
	ptrSize    = 8
)

// region 标识对象所在区域。
type region uint8

const (
	regionNone region = iota
	regionEden
	regionFrom
	regionTo
	regionOld
)

func (r region) String() string {
	switch r {
	case regionEden:
		return "eden"
	case regionFrom:
		return "survivor(from)"
	case regionTo:
		return "survivor(to)"
	case regionOld:
		return "old"
	default:
		return "none"
	}
}

// loc 是堆内位置：区域 + 区域内偏移。
type loc struct {
	region region
	offset uint32
}

var nullLoc = loc{region: regionNone}

func (l loc) isNull() bool { return l.region == regionNone }

func (l loc) String() string {
	if l.isNull() {
		return "null"
	}
	return fmt.Sprintf("%s@%d", l.region, l.offset)
}

func encodeLoc(l loc) []byte {
	b := make([]byte, ptrSize)
	b[0] = byte(l.region)
	binary.BigEndian.PutUint32(b[4:8], l.offset)
	return b
}

func decodeLoc(b []byte) loc {
	return loc{region: region(b[0]), offset: binary.BigEndian.Uint32(b[4:8])}
}

func objectSize(nfields int, payloadLen int) uint32 {
	return uint32(headerSize + nfields*ptrSize + payloadLen)
}

// obj 是某个对象在某区域内的布局视图。
type obj struct {
	loc        loc
	handleID   uint32
	age        uint16
	nfields    uint32
	payloadLen uint32
}

func (o obj) size() uint32 { return objectSize(int(o.nfields), int(o.payloadLen)) }

// Config 配置各代容量与晋升阈值（单位：字节）。
type Config struct {
	EdenSize     uint32
	SurvivorSize uint32 // 单个幸存区大小；两个幸存区等大
	OldSize      uint32
	PromoteAge   uint16 // 存活次数达到该阈值即晋升
}

// Heap 是一个分代复制式垃圾回收堆。
type Heap struct {
	mu sync.RWMutex

	cfg Config

	eden  []byte
	survA []byte
	survB []byte
	old   []byte

	used map[region]uint32

	// fromRegion/toRegion 分别为当前“存活幸存区”和“空闲幸存区”。
	fromRegion region
	toRegion   region

	// handles 把对外句柄映射到对象当前位置；被回收的句柄删除键。
	handles map[uint32]loc
	// roots 是调用方显式登记的根集。
	roots map[uint32]struct{}
	// remembered 记录“老年代字段引用年轻代对象”的老对象偏移；
	// 每次回收结束后依据实际跨代引用重建（升序迭代保证确定性）。
	remembered map[uint32]struct{}

	nextHandleID uint32
	minorGCs     uint64
	promotions   uint64

	// Logger 非空时，每个操作打印输入、输出与判定依据。
	Logger interface {
		Printf(format string, args ...any)
	}
}

// NewHeap 按配置创建空堆。
func NewHeap(cfg Config) *Heap {
	if cfg.PromoteAge == 0 {
		cfg.PromoteAge = 3
	}
	return &Heap{
		cfg:          cfg,
		eden:         make([]byte, cfg.EdenSize),
		survA:        make([]byte, cfg.SurvivorSize),
		survB:        make([]byte, cfg.SurvivorSize),
		old:          make([]byte, cfg.OldSize),
		used:         map[region]uint32{regionEden: 0, regionFrom: 0, regionTo: 0, regionOld: 0},
		fromRegion:   regionFrom,
		toRegion:     regionTo,
		handles:      map[uint32]loc{},
		roots:        map[uint32]struct{}{},
		remembered:   map[uint32]struct{}{},
		nextHandleID: 1,
	}
}

func (h *Heap) logf(format string, args ...any) {
	if h.Logger != nil {
		h.Logger.Printf(format, args...)
	}
}

// regionBuf 返回某区域当前的后备字节。
func (h *Heap) regionBuf(r region) []byte {
	switch r {
	case regionEden:
		return h.eden
	case regionFrom, regionTo:
		// survA 承载 fromRegion，survB 承载 toRegion，交换幸存区时整体换名。
		if h.fromRegion == regionFrom {
			if r == regionFrom {
				return h.survA
			}
			return h.survB
		}
		if r == regionFrom {
			return h.survB
		}
		return h.survA
	default:
		return h.old
	}
}

func isYoung(r region) bool { return r == regionEden || r == regionFrom || r == regionTo }

func (h *Heap) readObj(l loc) obj {
	b := h.regionBuf(l.region)
	p := l.offset
	return obj{
		loc:        l,
		handleID:   binary.BigEndian.Uint32(b[p : p+4]),
		age:        binary.BigEndian.Uint16(b[p+4 : p+6]),
		nfields:    binary.BigEndian.Uint32(b[p+6 : p+10]),
		payloadLen: binary.BigEndian.Uint32(b[p+10 : p+14]),
	}
}

func writeObjInto(buf []byte, at uint32, id uint32, age uint16, nfields uint32, fields []loc, payload []byte) {
	binary.BigEndian.PutUint32(buf[at:at+4], id)
	binary.BigEndian.PutUint16(buf[at+4:at+6], age)
	binary.BigEndian.PutUint32(buf[at+6:at+10], nfields)
	binary.BigEndian.PutUint32(buf[at+10:at+14], uint32(len(payload)))
	fp := at + headerSize
	for i := range fields {
		copy(buf[fp+uint32(i)*ptrSize:], encodeLoc(fields[i]))
	}
	copy(buf[fp+nfields*ptrSize:], payload)
}

func (h *Heap) fieldLoc(o obj, index int) loc {
	fp := o.loc.offset + headerSize + uint32(index)*ptrSize
	return decodeLoc(h.regionBuf(o.loc.region)[fp : fp+ptrSize])
}

func (h *Heap) setFieldLoc(o obj, index int, l loc) {
	fp := o.loc.offset + headerSize + uint32(index)*ptrSize
	copy(h.regionBuf(o.loc.region)[fp:fp+ptrSize], encodeLoc(l))
}

func (h *Heap) payloadBytes(o obj) []byte {
	start := o.loc.offset + headerSize + o.nfields*ptrSize
	return h.regionBuf(o.loc.region)[start : start+o.payloadLen]
}

func (h *Heap) resolve(handleID uint32) (obj, error) {
	l, ok := h.handles[handleID]
	if !ok {
		return obj{}, fmt.Errorf("%w: handle=%d", ErrHandleReclaimed, handleID)
	}
	return h.readObj(l), nil
}

// Alloc 在 Eden 分配对象；Eden 空间不足先做一次次要回收，仍不足再报错。
func (h *Heap) Alloc(nfields int, payload []byte) (uint32, error) {
	if nfields < 0 {
		return 0, fmt.Errorf("%w: nfields 不能为负", ErrFieldIndex)
	}
	pLen := 0
	if payload != nil {
		pLen = len(payload)
	}
	need := objectSize(nfields, pLen)

	h.mu.Lock()
	defer h.mu.Unlock()

	if need > h.cfg.EdenSize {
		h.logf("ALLOC 拒绝: nfields=%d payloadLen=%d need=%d > edenCap=%d 原因=%v",
			nfields, pLen, need, h.cfg.EdenSize, ErrPayloadTooLarge)
		return 0, fmt.Errorf("%w: 需要 %d 字节，Eden 容量 %d 字节", ErrPayloadTooLarge, need, h.cfg.EdenSize)
	}

	if h.used[regionEden]+need > h.cfg.EdenSize {
		h.logf("ALLOC 触发 MinorGC: edenUsed=%d need=%d", h.used[regionEden], need)
		if err := h.minorGCLocked(); err != nil {
			return 0, err
		}
	}

	id := h.nextHandleID
	h.nextHandleID++
	l := loc{region: regionEden, offset: h.used[regionEden]}
	fields := make([]loc, nfields)
	writeObjInto(h.eden, l.offset, id, 0, uint32(nfields), fields, payload)
	h.used[regionEden] += need
	h.handles[id] = l
	h.logf("ALLOC 输出: handle=%d loc=%s size=%d edenUsed=%d", id, l, need, h.used[regionEden])
	return id, nil
}

// AddRoot 把现存句柄登记为根。
func (h *Heap) AddRoot(handleID uint32) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.handles[handleID]; !ok {
		return fmt.Errorf("%w: handle=%d", ErrHandleReclaimed, handleID)
	}
	h.roots[handleID] = struct{}{}
	h.logf("ADDROOT: handle=%d roots=%d", handleID, len(h.roots))
	return nil
}

// RemoveRoot 取消根登记；句柄不存在时静默。
func (h *Heap) RemoveRoot(handleID uint32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.roots, handleID)
	h.logf("REMOVEROOT: handle=%d roots=%d", handleID, len(h.roots))
}

// SetField 写引用字段；target 为 0 表示清空。
// 写屏障：“老年代对象 -> 年轻代对象”时把老对象加入记忆集。
func (h *Heap) SetField(handleID uint32, index int, target uint32) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	o, err := h.resolve(handleID)
	if err != nil {
		h.logf("SETFIELD 拒绝: handle=%d index=%d 原因=%v", handleID, index, err)
		return err
	}
	if index < 0 || index >= int(o.nfields) {
		h.logf("SETFIELD 拒绝: handle=%d index=%d nfields=%d 原因=%v",
			handleID, index, o.nfields, ErrFieldIndex)
		return fmt.Errorf("%w: index=%d nfields=%d", ErrFieldIndex, index, o.nfields)
	}
	tl := nullLoc
	if target != 0 {
		tlo, ok := h.handles[target]
		if !ok {
			h.logf("SETFIELD 拒绝: target=%d 原因=%v", target, ErrHandleReclaimed)
			return fmt.Errorf("%w: target=%d", ErrHandleReclaimed, target)
		}
		tl = tlo
	}
	h.setFieldLoc(o, index, tl)
	if o.loc.region == regionOld && isYoung(tl.region) {
		h.remembered[o.loc.offset] = struct{}{}
		h.logf("SETFIELD 写屏障: old@%d -> young %s，加入记忆集 size=%d",
			o.loc.offset, tl, len(h.remembered))
	} else {
		h.logf("SETFIELD: handle=%d[%d]=%d (%s)", handleID, index, target, tl)
	}
	return nil
}

// GetField 读引用字段；ok 为 false 表示字段为空。
func (h *Heap) GetField(handleID uint32, index int) (target uint32, ok bool, err error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	o, err := h.resolve(handleID)
	if err != nil {
		return 0, false, err
	}
	if index < 0 || index >= int(o.nfields) {
		return 0, false, fmt.Errorf("%w: index=%d nfields=%d", ErrFieldIndex, index, o.nfields)
	}
	l := h.fieldLoc(o, index)
	if l.isNull() {
		return 0, false, nil
	}
	return h.readObj(l).handleID, true, nil
}

// SetPayload 覆盖对象载荷，长度不可变大。
func (h *Heap) SetPayload(handleID uint32, payload []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	o, err := h.resolve(handleID)
	if err != nil {
		return err
	}
	if len(payload) > int(o.payloadLen) {
		return fmt.Errorf("%w: 新载荷 %d > 已分配 %d", ErrPayloadTooLarge, len(payload), o.payloadLen)
	}
	dst := h.payloadBytes(o)
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, payload)
	h.logf("SETPAYLOAD: handle=%d len=%d", handleID, len(payload))
	return nil
}

// Payload 返回对象载荷的副本。
func (h *Heap) Payload(handleID uint32) ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	o, err := h.resolve(handleID)
	if err != nil {
		return nil, err
	}
	return slices.Clone(h.payloadBytes(o)), nil
}

// Live 报告句柄是否仍指向存活对象。
func (h *Heap) Live(handleID uint32) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.handles[handleID]
	return ok
}

// Stats 返回各代已用字节与回收/晋升计数。
type Stats struct {
	EdenUsed     uint32
	SurvivorUsed uint32
	OldUsed      uint32
	MinorGCs     uint64
	Promotions   uint64
}

func (h *Heap) Stats() Stats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Stats{
		EdenUsed:     h.used[regionEden],
		SurvivorUsed: h.used[h.fromRegion],
		OldUsed:      h.used[regionOld],
		MinorGCs:     h.minorGCs,
		Promotions:   h.promotions,
	}
}

// MinorGCCount / PromotionCount 暴露确定性计数。
func (h *Heap) MinorGCCount() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.minorGCs
}

func (h *Heap) PromotionCount() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.promotions
}

// MinorGC 显式触发一次次要回收。
func (h *Heap) MinorGC() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.minorGCLocked()
}
