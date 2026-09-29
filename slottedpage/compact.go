package slottedpage

// compact 在整页副本上重建“槽目录 + 紧挨尾部的记录区”。
//
// newSlotCount 为整理后的槽数（插入新槽时比当前多 1）；
// replacement 给出参与重排的新记录或被更新记录（按槽编号索引）。
// 整理后编号越小的记录越靠页尾、彼此紧挨；整个过程在副本上完成，
// 成功后才一次性替换当前映像，读者因此不可能看到半移动状态。
func (p *Page) compact(newSlotCount int, replacement map[int][]byte) error {
	type liveRec struct {
		id   int
		data []byte
	}

	recs := make([]liveRec, 0, newSlotCount)
	totalRecord := 0
	for i := 0; i < newSlotCount; i++ {
		if d, ok := replacement[i]; ok {
			recs = append(recs, liveRec{id: i, data: d})
			totalRecord += len(d)
			continue
		}
		if i < p.slotCount && p.slotLength(i) != 0 {
			l := p.slotLength(i)
			o := p.slotOffset(i)
			d := make([]byte, l)
			copy(d, p.buf[o:o+l])
			recs = append(recs, liveRec{id: i, data: d})
			totalRecord += l
		}
	}

	newDirEnd := p.headerSize + newSlotCount*p.slotSize
	if newDirEnd+totalRecord > p.pageSize {
		return ErrInsufficientSpace
	}

	nb := make([]byte, p.pageSize)

	// 槽目录：所有槽先置空，再按记录落点填写。
	off := p.pageSize
	// recs 按 id 升序；从页尾开始依次放置，使编号越小越靠页尾。
	for _, r := range recs {
		off -= len(r.data)
		copy(nb[off:off+len(r.data)], r.data)
		b := p.headerSize + r.id*p.slotSize
		putSlot(nb, b, off, len(r.data))
	}

	old := p.buf
	oldSlots := p.slotCount
	oldFreeEnd := p.freeEnd
	oldRecordBytes := p.recordBytes
	oldCompactions := p.compactions

	p.buf = nb
	p.slotCount = newSlotCount
	p.freeEnd = off
	p.recordBytes = totalRecord
	p.compactions++
	p.writeHeader()

	// 提交点之后的状态恒为自洽；前面任何 panic 前都未触碰可见字段，
	// 而 errInsufficientSpace 的判断已在上游完成，此处仅做防御性回滚。
	if p.freeEnd < newDirEnd {
		p.buf = old
		p.slotCount = oldSlots
		p.freeEnd = oldFreeEnd
		p.recordBytes = oldRecordBytes
		p.compactions = oldCompactions
		return ErrInsufficientSpace
	}
	return nil
}

func putSlot(buf []byte, base, offset, length int) {
	buf[base] = byte(offset >> 24)
	buf[base+1] = byte(offset >> 16)
	buf[base+2] = byte(offset >> 8)
	buf[base+3] = byte(offset)
	buf[base+4] = byte(length >> 24)
	buf[base+5] = byte(length >> 16)
	buf[base+6] = byte(length >> 8)
	buf[base+7] = byte(length)
}
