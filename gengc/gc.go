package gengc

import (
	"encoding/binary"
	"slices"
)

// forwardingMarker 写在源对象首字节，表示该对象已被复制，
// 其后 7 字节记录目标 loc（region + offset）。
const forwardingMarker = 0xFF

// snapshot 是回收前堆状态的逐字节快照，晋升失败时整体撤回。
type snapshot struct {
	eden, from, old []byte
	used            map[region]uint32
	handles         map[uint32]loc
	remembered      map[uint32]struct{}
	minorGCs        uint64
	promotions      uint64
}

func (h *Heap) takeSnapshot() snapshot {
	return snapshot{
		eden:       slices.Clone(h.eden),
		from:       slices.Clone(h.regionBuf(h.fromRegion)),
		old:        slices.Clone(h.old),
		used:       mapsCloneUint32(h.used),
		handles:    mapsCloneLoc(h.handles),
		remembered: mapsCloneSet(h.remembered),
		minorGCs:   h.minorGCs,
		promotions: h.promotions,
	}
}

func mapsCloneUint32(m map[region]uint32) map[region]uint32 {
	out := make(map[region]uint32, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mapsCloneLoc(m map[uint32]loc) map[uint32]loc {
	out := make(map[uint32]loc, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mapsCloneSet(m map[uint32]struct{}) map[uint32]struct{} {
	out := make(map[uint32]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func sortedKeys(m map[uint32]struct{}) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// writeForwarding 在源对象头部留转发地址。
func writeForwarding(srcBuf []byte, srcOff uint32, dst loc) {
	srcBuf[srcOff] = forwardingMarker
	srcBuf[srcOff+1] = byte(dst.region)
	binary.BigEndian.PutUint32(srcBuf[srcOff+3:srcOff+7], dst.offset)
}

// minorGCLocked 执行一次次要回收，调用时须持有 h.mu 写锁。
//
// 起点：根集 + 记忆集（老 -> 幼）。
// 年轻可达对象复制到空闲幸存区，存活次数达阈值或幸存区放不下时晋升；
// 老年代同时做一次内部整理（复制到全新缓冲区）。
// 任何晋升分配失败则放弃所有新缓冲区，恢复快照，堆逐字节回到回收前。
func (h *Heap) minorGCLocked() error {
	snap := h.takeSnapshot()

	toBuf := make([]byte, h.cfg.SurvivorSize)
	newOld := make([]byte, h.cfg.OldSize)
	var toUsed, oldUsed uint32

	fwd := map[loc]loc{}
	var queue []loc // 待扫描字段的“目标位置”

	srcBuf := func(r region) []byte {
		switch r {
		case regionEden:
			return h.eden
		case regionFrom:
			return h.regionBuf(h.fromRegion)
		default:
			return h.old
		}
	}

	// evacuate 把源对象复制到目标区域；已复制则直接返回转发地址。
	// 第二个返回值为 false 表示晋升失败，本次回收必须撤回。
	evacuate := func(src loc) (loc, bool) {
		if dst, ok := fwd[src]; ok {
			return dst, true
		}
		sb := srcBuf(src.region)
		if sb[src.offset] == forwardingMarker {
			// 理论上 map 已覆盖；防御性地读取转发地址。
			dst := loc{region: region(sb[src.offset+1]), offset: binary.BigEndian.Uint32(sb[src.offset+3 : src.offset+7])}
			fwd[src] = dst
			return dst, true
		}
		o := h.readObj(src)

		var dst loc
		promote := false
		if src.region == regionOld {
			promote = true // 老年代整理：仍然落到老年代
		} else {
			newAge := o.age + 1
			if newAge >= h.cfg.PromoteAge {
				promote = true // 存活次数达阈值
			} else if toUsed+o.size() > h.cfg.SurvivorSize {
				promote = true // 幸存区放不下，提前晋升
			}
		}

		switch {
		case src.region == regionOld:
			if oldUsed+o.size() > h.cfg.OldSize {
				return loc{}, false
			}
			dst = loc{region: regionOld, offset: oldUsed}
			copy(newOld[oldUsed:oldUsed+o.size()], sb[src.offset:src.offset+o.size()])
			oldUsed += o.size()
		case promote:
			if oldUsed+o.size() > h.cfg.OldSize {
				h.logf("GC 晋升失败: handle=%d size=%d oldUsed=%d oldCap=%d 原因=%v",
					o.handleID, o.size(), oldUsed, h.cfg.OldSize, ErrOldGenFull)
				return loc{}, false
			}
			dst = loc{region: regionOld, offset: oldUsed}
			copy(newOld[oldUsed:oldUsed+o.size()], sb[src.offset:src.offset+o.size()])
			// 年龄改写为晋升后的值。
			binary.BigEndian.PutUint16(newOld[dst.offset+4:dst.offset+6], o.age+1)
			oldUsed += o.size()
			h.promotions++
			h.logf("GC 晋升: handle=%d %s age=%d -> old@%d size=%d%s",
				o.handleID, src, o.age, dst.offset, o.size(),
				gcPromoteReason(o.age+1 >= h.cfg.PromoteAge))
		default:
			dst = loc{region: regionTo, offset: toUsed}
			copy(toBuf[toUsed:toUsed+o.size()], sb[src.offset:src.offset+o.size()])
			binary.BigEndian.PutUint16(toBuf[dst.offset+4:dst.offset+6], o.age+1)
			toUsed += o.size()
			h.logf("GC 复制: handle=%d %s age=%d -> %s size=%d",
				o.handleID, src, o.age, dst, o.size())
		}

		fwd[src] = dst
		writeForwarding(sb, src.offset, dst)
		queue = append(queue, dst)
		return dst, true
	}

	// rewritePtr 把目标缓冲区中的一个源指针改写到新位置。
	rewritePtr := func(dstBuf []byte, fp uint32) bool {
		l := decodeLoc(dstBuf[fp : fp+ptrSize])
		if l.isNull() {
			return true
		}
		nl, ok := evacuate(l)
		if !ok {
			return false
		}
		copy(dstBuf[fp:fp+ptrSize], encodeLoc(nl))
		return true
	}

	// 1) 根集为起点（升序，保证操作序列确定）。
	rootIDs := sortedKeys(h.roots)
	for _, id := range rootIDs {
		src, ok := h.handles[id]
		if !ok {
			delete(h.roots, id)
			continue
		}
		if _, ok := evacuate(src); !ok {
			h.rollback(snap)
			return ErrOldGenFull
		}
	}

	// 2) 记忆集为起点：老年代对象（含只被老年代引用链保活的）。
	for _, off := range sortedKeys(h.remembered) {
		if off >= h.used[regionOld] {
			continue
		}
		if _, ok := evacuate(loc{region: regionOld, offset: off}); !ok {
			h.rollback(snap)
			return ErrOldGenFull
		}
	}

	// 3) Cheney 式扫描：宽度优先复制所有引用目标并改写指针。
	for qi := 0; qi < len(queue); qi++ {
		dst := queue[qi]
		var db []byte
		switch dst.region {
		case regionTo:
			db = toBuf
		default:
			db = newOld
		}
		o := readObjFrom(db, dst.offset)
		for i := uint32(0); i < o.nfields; i++ {
			fp := dst.offset + headerSize + i*ptrSize
			if !rewritePtr(db, fp) {
				h.rollback(snap)
				return ErrOldGenFull
			}
		}
	}

	// 4) 提交：新幸存区上位为 From，再分配空 To；老年代换新区。
	newTo := make([]byte, h.cfg.SurvivorSize)
	// 复制时目标标签为 regionTo（临时缓冲 toBuf）；toBuf 即将成为存活的
	// From，故缓冲内所有指向 regionTo 的字段都改标为 fromRegion。
	for off := uint32(0); off < toUsed; {
		o := readObjFrom(toBuf, off)
		for i := uint32(0); i < o.nfields; i++ {
			fp := off + headerSize + i*ptrSize
			l := decodeLoc(toBuf[fp : fp+ptrSize])
			if l.region == regionTo {
				l.region = h.fromRegion
				copy(toBuf[fp:fp+ptrSize], encodeLoc(l))
			}
		}
		off += o.size()
	}
	h.survA = toBuf
	h.survB = newTo
	h.fromRegion = regionFrom
	h.toRegion = regionTo
	h.old = newOld

	for i := range h.eden {
		h.eden[i] = 0
	}
	h.used[regionEden] = 0
	h.used[regionFrom] = toUsed
	h.used[regionTo] = 0
	h.used[regionOld] = oldUsed

	// 5) 句柄表按转发地址更新；未被复制的对象不可达，句柄失效。
	newHandles := map[uint32]loc{}
	for _, id := range sortedHandleIDs(h.handles) {
		src := h.handles[id]
		if dst, ok := fwd[src]; ok {
			if dst.region == regionTo {
				dst.region = h.fromRegion
			}
			newHandles[id] = dst
		} else {
			h.logf("GC 回收: handle=%d loc=%s 不可达", id, src)
		}
	}
	h.handles = newHandles

	// 6) 重建记忆集：线性扫描老年代，凡字段指向年轻代即记录；
	//    不再指向年轻代的老对象自然移出。
	newRemembered := map[uint32]struct{}{}
	for off := uint32(0); off < oldUsed; {
		o := readObjFrom(newOld, off)
		for i := uint32(0); i < o.nfields; i++ {
			fp := off + headerSize + i*ptrSize
			l := decodeLoc(newOld[fp : fp+ptrSize])
			if l.region == regionTo {
				l.region = h.fromRegion
				copy(newOld[fp:fp+ptrSize], encodeLoc(l))
			}
			if isYoung(l.region) {
				newRemembered[off] = struct{}{}
				h.logf("GC 记忆集维护: old@%d(handle=%d) 字段%d -> young %s，保留",
					off, o.handleID, i, l)
			}
		}
		off += o.size()
	}
	h.remembered = newRemembered

	h.minorGCs++
	h.logf("GC 完成: #%d 晋升累计=%d edenUsed=0 survivorUsed=%d oldUsed=%d 记忆集=%d",
		h.minorGCs, h.promotions, toUsed, oldUsed, len(newRemembered))
	return nil
}

func gcPromoteReason(byAge bool) string {
	if byAge {
		return "（原因：存活次数达阈值）"
	}
	return "（原因：幸存区溢出提前晋升）"
}

// readObjFrom 从任意缓冲区读对象头。
func readObjFrom(b []byte, p uint32) obj {
	return obj{
		loc:        loc{offset: p},
		handleID:   binary.BigEndian.Uint32(b[p : p+4]),
		age:        binary.BigEndian.Uint16(b[p+4 : p+6]),
		nfields:    binary.BigEndian.Uint32(b[p+6 : p+10]),
		payloadLen: binary.BigEndian.Uint32(b[p+10 : p+14]),
	}
}

func sortedHandleIDs(m map[uint32]loc) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// rollback 把堆恢复到快照状态（逐字节相同）。
func (h *Heap) rollback(s snapshot) {
	copy(h.eden, s.eden)
	from := h.regionBuf(h.fromRegion)
	copy(from, s.from)
	h.old = slices.Clone(s.old)
	h.used = mapsCloneUint32(s.used)
	h.handles = mapsCloneLoc(s.handles)
	h.remembered = mapsCloneSet(s.remembered)
	h.minorGCs = s.minorGCs
	h.promotions = s.promotions
	h.logf("GC 撤回: 已恢复回收前快照，堆逐字节不变")
}
