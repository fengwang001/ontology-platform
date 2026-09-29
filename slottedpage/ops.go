package slottedpage

// Insert 插入一条记录，返回稳定的槽编号。
// 优先复用编号最小的空槽，无空槽时才在目录末尾新增槽项。
// 被拒绝时页内字节保持不变。
func (p *Page) Insert(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(data) == 0 {
		return 0, ErrEmptyRecord
	}
	if len(data)+p.slotSize > p.pageSize-p.headerSize {
		return 0, ErrRecordTooLarge
	}

	target := -1
	for i := 0; i < p.slotCount; i++ {
		if p.slotLength(i) == 0 {
			target = i
			break
		}
	}
	grow := target == -1
	if grow {
		target = p.slotCount
	}

	extraSlot := 0
	if grow {
		extraSlot = p.slotSize
	}
	dirEnd := p.headerSize + p.slotCount*p.slotSize

	// 连续空闲区直接够用：绝不整理。
	if p.freeEnd-dirEnd >= len(data)+extraSlot {
		if grow {
			p.slotCount++
		}
		off := p.freeEnd - len(data)
		copy(p.buf[off:off+len(data)], data)
		p.writeSlot(target, off, len(data))
		p.freeEnd = off
		p.recordBytes += len(data)
		p.writeHeader()
		return target, nil
	}

	// 连续区不够：看全页可用字节（含碎片，以及新增槽项的占用）。
	totalSlots := p.slotCount
	if grow {
		totalSlots++
	}
	available := p.pageSize - p.headerSize - totalSlots*p.slotSize - p.recordBytes
	if available < len(data) {
		return 0, ErrInsufficientSpace
	}

	// 在副本上一次性完成“含新记录的整理”，完成后编号越小越靠页尾。
	if err := p.compact(totalSlots, map[int][]byte{target: data}); err != nil {
		return 0, err
	}
	return target, nil
}

// Update 以新内容替换槽 id 中的记录。可原地容纳时原地处理；
// 原地放不下但全页可用字节足够时整理后放入；仍不足则拒绝且页不变。
func (p *Page) Update(id int, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(data) == 0 {
		return ErrEmptyRecord
	}
	if len(data)+p.slotSize > p.pageSize-p.headerSize {
		return ErrRecordTooLarge
	}
	if id < 0 || id >= p.slotCount {
		return ErrInvalidSlotID
	}
	oldOff := p.slotOffset(id)
	oldLen := p.slotLength(id)
	if oldLen == 0 {
		return ErrEmptySlot
	}

	newLen := len(data)
	dirEnd := p.headerSize + p.slotCount*p.slotSize

	switch {
	case newLen == oldLen:
		copy(p.buf[oldOff:oldOff+oldLen], data)
		return nil
	case newLen < oldLen:
		// 向后对齐缩短，释放出的前导字节清零并并入空闲/碎片区。
		newOff := oldOff + oldLen - newLen
		copy(p.buf[newOff:newOff+newLen], data)
		for i := oldOff; i < newOff; i++ {
			p.buf[i] = 0
		}
		p.writeSlot(id, newOff, newLen)
		if oldOff == p.freeEnd {
			p.freeEnd = newOff
		}
		p.recordBytes += newLen - oldLen
		p.writeHeader()
		return nil
	}

	// newLen > oldLen：该记录是最低记录且前方连续区足够时，原地向前扩展。
	grow := newLen - oldLen
	if oldOff == p.freeEnd && oldOff-dirEnd >= newLen {
		newOff := oldOff - grow
		// 整体前移旧内容（memmove 支持源/目的重叠），再写入新数据。
		copy(p.buf[newOff:newOff+oldLen], p.buf[oldOff:oldOff+oldLen])
		copy(p.buf[newOff:newOff+newLen], data)
		p.writeSlot(id, newOff, newLen)
		p.freeEnd = newOff
		p.recordBytes += grow
		p.writeHeader()
		return nil
	}

	// 原地放不下：全页可用字节足够则整理（新内容参与重排），否则拒绝。
	if p.pageSize-p.headerSize-p.slotCount*p.slotSize-(p.recordBytes-oldLen) < newLen {
		return ErrInsufficientSpace
	}
	return p.compact(p.slotCount, map[int][]byte{id: data})
}

// Delete 将槽置空。若该槽位于目录末尾，则连同其前方连续的空槽一并收回；
// 被删记录占用的字节立即清零。
func (p *Page) Delete(id int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if id < 0 || id >= p.slotCount {
		return ErrInvalidSlotID
	}
	off := p.slotOffset(id)
	length := p.slotLength(id)
	if length == 0 {
		return ErrEmptySlot
	}

	for i := off; i < off+length; i++ {
		p.buf[i] = 0
	}
	p.writeSlot(id, 0, 0)
	p.recordBytes -= length

	if id == p.slotCount-1 {
		for p.slotCount > 0 && p.slotLength(p.slotCount-1) == 0 {
			base := p.headerSize + (p.slotCount-1)*p.slotSize
			for i := base; i < base+p.slotSize; i++ {
				p.buf[i] = 0
			}
			p.slotCount--
		}
	}

	p.freeEnd = p.pageSize
	for i := 0; i < p.slotCount; i++ {
		if l := p.slotLength(i); l != 0 {
			if o := p.slotOffset(i); o < p.freeEnd {
				p.freeEnd = o
			}
		}
	}
	p.writeHeader()
	return nil
}

// Get 返回槽 id 中记录的副本；读者与写者可并发，且看不到整理中的状态。
func (p *Page) Get(id int) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if id < 0 || id >= p.slotCount {
		return nil, ErrInvalidSlotID
	}
	length := p.slotLength(id)
	if length == 0 {
		return nil, ErrEmptySlot
	}
	off := p.slotOffset(id)
	out := make([]byte, length)
	copy(out, p.buf[off:off+length])
	return out, nil
}
