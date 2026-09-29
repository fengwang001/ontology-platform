package gencopy

import (
	"encoding/binary"
	"sort"
)

// toRegionID 返回当前 To 幸存区的区域 ID。
func toRegionID(r *region) int {
	if r.name == "survivor-A" {
		return regionSurvA
	}
	return regionSurvB
}

func binaryPutAge(hdr []byte, age uint32) {
	binary.LittleEndian.PutUint32(hdr[hdrAge:], age)
}

// minorGC 执行一次次要回收（young/minor collection）。调用方必须持有 h.mu。
//
// 回收规则：
//   - 根集与记忆集（老年代 -> 年轻代引用）作为本次回收的 GC Roots。
//   - 可达年轻对象用 Cheney 半空间复制算法复制到空闲幸存区；
//     每个对象只复制一次，原对象头中留下转发地址。
//   - 存活后年龄达到 PromoteAge，或空闲幸存区放不下时，对象晋升到老年代。
//   - 老年代放不下晋升对象时返回 ErrOldFull，整堆恢复到回收前的字节状态，
//     本次回收整体撤回。
//   - 提交时原子切换 Eden/幸存区/老年代游标、重定位句柄表、
//     为不可达年轻对象立墓碑，并精确重建记忆集。
func (h *Heap) minorGC() error {
	// 整堆快照：回收期间一切写入都直接落在 h.mem 上，失败时整体还原，
	// 保证“堆与回收前逐字节相同”。
	snapshot := make([]byte, len(h.mem))
	copy(snapshot, h.mem)

	to := h.to
	from := h.from

	toCursor := 0
	oldCursor := h.old.used
	promoted := 0

	// 回收期间同时认识旧地址与新地址 -> 句柄。
	addrToHdl := make(map[int]Handle, len(h.byAddr))
	for a, hd := range h.byAddr {
		addrToHdl[a] = hd
	}

	// queue 是 Cheney 扫描队列：已疏散、尚待扫描其引用字段的对象。
	type qitem struct {
		hdl Handle
		abs int // 疏散后的新绝对地址
	}
	var queue []qitem

	// fwdInfo 记录每个被疏散对象的最终去向，提交时重定位句柄表。
	type fwdInfo struct {
		targetRegion int
		newOff       int
		promoted     bool
	}
	forwarded := make(map[Handle]*fwdInfo)

	// evac 把一个年轻对象疏散到幸存区或老年代，返回疏散后地址。
	evac := func(hdl Handle) (int, error) {
		src := h.objs[hdl]
		srcAddr := h.abs(src)
		hdr := h.mem[srcAddr : srcAddr+headerSize]

		if hdr[hdrFwdGen] != genNone {
			return int(getU64(hdr[hdrFwdAddr:])), nil
		}

		nrefs := int(binary.LittleEndian.Uint32(hdr[hdrNumRefs:]))
		plen := int(binary.LittleEndian.Uint32(hdr[hdrPayloadLen:]))
		size := objectSize(nrefs, plen)
		newAge := int(binary.LittleEndian.Uint32(hdr[hdrAge:])) + 1

		targetRegion := regionOld
		var dstAddr int

		if newAge >= h.promoteAge {
			// 存活次数达阈值：晋升。
			if oldCursor+size > h.old.size {
				return 0, ErrOldFull
			}
			dstAddr = h.old.start + oldCursor
			oldCursor += size
			promoted++
		} else if toCursor+size <= to.size {
			// 复制到空闲幸存区。
			dstAddr = to.start + toCursor
			toCursor += size
			targetRegion = toRegionID(to)
		} else {
			// 幸存区放不下：提前晋升（不论年龄）。
			if oldCursor+size > h.old.size {
				return 0, ErrOldFull
			}
			dstAddr = h.old.start + oldCursor
			oldCursor += size
			promoted++
		}

		// 复制对象（头 + 引用槽 + 载荷）到目标位置。
		copy(h.mem[dstAddr:dstAddr+size], h.mem[srcAddr:srcAddr+size])
		dstHdr := h.mem[dstAddr : dstAddr+headerSize]
		binaryPutAge(dstHdr, uint32(newAge))
		dstHdr[hdrFwdGen] = genNone
		putU64(dstHdr[hdrFwdAddr:], nilAddr)

		// 原对象留转发地址，保证同一对象只复制一次。
		if targetRegion == regionOld {
			hdr[hdrFwdGen] = genOld
		} else {
			hdr[hdrFwdGen] = genYoung
		}
		putU64(hdr[hdrFwdAddr:], uint64(dstAddr))

		addrToHdl[dstAddr] = hdl
		if targetRegion == regionOld {
			forwarded[hdl] = &fwdInfo{targetRegion: regionOld, newOff: dstAddr - h.old.start, promoted: true}
		} else {
			forwarded[hdl] = &fwdInfo{targetRegion: toRegionID(to), newOff: dstAddr - to.start}
		}
		queue = append(queue, qitem{hdl: hdl, abs: dstAddr})
		return dstAddr, nil
	}

	// scanRef 处理某个对象中的一个引用槽：年轻目标则疏散并改写到新地址。
	scanRef := func(holderAddr int, slot int) error {
		p := holderAddr + headerSize + 8*slot
		v := getU64(h.mem[p:])
		if v == nilAddr {
			return nil
		}
		hdl := addrToHdl[int(v)]
		if hdl == Nil {
			// 槽位指向不可达对象曾占用的地址（例如引用了未登记根的对象，
			// 而该对象此前所在地址恰好与某存活地址重合的情形不会出现：
			// 地址在同一轮回收前唯一）。按悬空引用处理：清空。
			putU64(h.mem[p:], nilAddr)
			return nil
		}
		// 老年代对象不移动：老 -> 老引用保持原样。
		if !h.isYoung(h.objs[hdl]) {
			return nil
		}
		newAddr, err := evac(hdl)
		if err != nil {
			return err
		}
		putU64(h.mem[p:], uint64(newAddr))
		return nil
	}

	// 1) 根集（按句柄升序，保证确定性）。
	for _, r := range h.roots {
		l := h.objs[r]
		if l == nil || !h.isYoung(l) {
			continue // 老年代根不移动；根本身的句柄无需改写
		}
		if _, err := evac(r); err != nil {
			h.rollback(snapshot)
			return err
		}
	}

	// 2) 记忆集：所有持有年轻代引用的老年对象。
	for _, oh := range h.rs {
		l := h.objs[oh]
		if l == nil {
			continue
		}
		addr := h.abs(l)
		for i := 0; i < h.numRefsAt(addr); i++ {
			if err := scanRef(addr, i); err != nil {
				h.rollback(snapshot)
				return err
			}
		}
	}

	// 3) Cheney 扫描：顺着已疏散对象的引用字段传递闭包。
	for qi := 0; qi < len(queue); qi++ {
		item := queue[qi]
		n := h.numRefsAt(item.abs)
		for i := 0; i < n; i++ {
			if err := scanRef(item.abs, i); err != nil {
				h.rollback(snapshot)
				return err
			}
		}
	}

	// ---- 提交（不会再失败；持锁状态下对外仍不可见中间态）----

	// 3.1 重定位句柄表；不可达年轻对象立墓碑。
	newObjs := make(map[Handle]*loc, len(h.objs))
	newByAddr := make(map[int]Handle, len(h.objs))
	for hdl, l := range h.objs {
		if fi, moved := forwarded[hdl]; moved {
			nl := &loc{region: fi.targetRegion, off: fi.newOff}
			newObjs[hdl] = nl
			var start int
			if fi.targetRegion == regionOld {
				start = h.old.start
			} else {
				start = to.start
			}
			newByAddr[start+fi.newOff] = hdl
			continue
		}
		if h.isYoung(l) {
			// 年轻代不可达对象：回收。
			h.tombstones[hdl] = struct{}{}
			continue
		}
		// 老年代对象不动。
		newObjs[hdl] = l
		newByAddr[h.abs(l)] = hdl
	}
	h.objs = newObjs
	h.byAddr = newByAddr

	// 3.2 切换代游标与幸存区角色。
	h.eden.used = 0
	from.used = 0
	to.used = toCursor
	h.old.used = oldCursor
	h.from, h.to = to, from

	// 3.3 精确重建记忆集：扫描全部老年对象（含本次晋升者），
	// 收集仍持有年轻代引用者，按句柄升序。
	newRS := make([]Handle, 0, len(h.rs))
	var oldHandles []Handle
	for hdl, l := range h.objs {
		if l.region == regionOld {
			oldHandles = append(oldHandles, hdl)
		}
	}
	sort.Slice(oldHandles, func(i, j int) bool { return oldHandles[i] < oldHandles[j] })
	for _, hdl := range oldHandles {
		addr := h.abs(h.objs[hdl])
		n := h.numRefsAt(addr)
		hasYoung := false
		for i := 0; i < n; i++ {
			v := getU64(h.mem[addr+headerSize+8*i:])
			if v != nilAddr && v < uint64(h.old.start) {
				hasYoung = true
				break
			}
		}
		if hasYoung {
			newRS = append(newRS, hdl)
		}
	}
	h.rs = newRS

	h.minorCount++
	h.promoteCount += promoted
	return nil
}

// rollback 把整堆内存恢复为回收前的字节快照。
// 回收过程中不修改任何映射、游标、计数，因此还原内存即完全撤回。
func (h *Heap) rollback(snapshot []byte) {
	copy(h.mem, snapshot)
}
