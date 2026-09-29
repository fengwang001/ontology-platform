package slottedpage

import "encoding/binary"

// New 创建一个空页；data 非空时则将其视为由 Marshal 产生的映像并还原。
func New(cfg Config, data []byte) (*Page, error) {
	if cfg.PageSize <= 0 || cfg.HeaderSize < minHeaderSize ||
		cfg.SlotSize < minSlotSize || cfg.HeaderSize >= cfg.PageSize {
		return nil, errInvalidConfig
	}
	p := &Page{
		buf:        make([]byte, cfg.PageSize),
		pageSize:   cfg.PageSize,
		headerSize: cfg.HeaderSize,
		slotSize:   cfg.SlotSize,
		freeEnd:    cfg.PageSize,
	}
	if len(data) != 0 {
		if err := p.load(data); err != nil {
			return nil, err
		}
		return p, nil
	}
	p.writeHeader()
	return p, nil
}

func (p *Page) writeHeader() {
	copy(p.buf[offMagic:offMagic+4], magic[:])
	binary.BigEndian.PutUint32(p.buf[offPageSize:], uint32(p.pageSize))
	binary.BigEndian.PutUint32(p.buf[offHeaderSize:], uint32(p.headerSize))
	binary.BigEndian.PutUint32(p.buf[offSlotSize:], uint32(p.slotSize))
	binary.BigEndian.PutUint32(p.buf[offSlotCount:], uint32(p.slotCount))
	binary.BigEndian.PutUint32(p.buf[offFreeEnd:], uint32(p.freeEnd))
	binary.BigEndian.PutUint32(p.buf[offCompactions:], uint32(p.compactions))
}

func (p *Page) slotBase(id int) int { return p.headerSize + id*p.slotSize }

func (p *Page) slotOffset(id int) int {
	b := p.slotBase(id)
	return int(binary.BigEndian.Uint32(p.buf[b:]))
}

func (p *Page) slotLength(id int) int {
	b := p.slotBase(id)
	return int(binary.BigEndian.Uint32(p.buf[b+4:]))
}

func (p *Page) writeSlot(id, offset, length int) {
	b := p.slotBase(id)
	for i := b; i < b+p.slotSize; i++ {
		p.buf[i] = 0
	}
	binary.BigEndian.PutUint32(p.buf[b:], uint32(offset))
	binary.BigEndian.PutUint32(p.buf[b+4:], uint32(length))
}

// load 校验并吸收一个页映像；任何校验失败都不改变接收者已提交的状态。
func (p *Page) load(data []byte) error {
	if len(data) != p.pageSize {
		return errInvalidImage
	}
	for i := 0; i < 4; i++ {
		if data[i] != magic[i] {
			return errInvalidImage
		}
	}
	if int(binary.BigEndian.Uint32(data[offPageSize:])) != p.pageSize ||
		int(binary.BigEndian.Uint32(data[offHeaderSize:])) != p.headerSize ||
		int(binary.BigEndian.Uint32(data[offSlotSize:])) != p.slotSize {
		return errInvalidImage
	}
	slotCount := int(binary.BigEndian.Uint32(data[offSlotCount:]))
	freeEnd := int(binary.BigEndian.Uint32(data[offFreeEnd:]))
	if slotCount < 0 || slotCount > p.pageSize || freeEnd < 0 || freeEnd > p.pageSize {
		return errInvalidImage
	}
	dirEnd := p.headerSize + slotCount*p.slotSize
	if dirEnd > p.pageSize {
		return errInvalidImage
	}

	recordBytes := 0
	minOff := p.pageSize
	seen := make([]bool, p.pageSize)
	for i := 0; i < slotCount; i++ {
		b := p.headerSize + i*p.slotSize
		for j := b + minSlotSize; j < b+p.slotSize; j++ {
			if data[j] != 0 {
				return errInvalidImage
			}
		}
		off := int(binary.BigEndian.Uint32(data[b:]))
		length := int(binary.BigEndian.Uint32(data[b+4:]))
		if length == 0 {
			if off != 0 {
				return errInvalidImage
			}
			continue
		}
		if off < dirEnd || off+length > p.pageSize {
			return errInvalidImage
		}
		for j := off; j < off+length; j++ {
			if seen[j] {
				return errInvalidImage
			}
			seen[j] = true
		}
		recordBytes += length
		if off < minOff {
			minOff = off
		}
	}
	if minOff != freeEnd {
		return errInvalidImage
	}
	if p.pageSize-p.headerSize-slotCount*p.slotSize-recordBytes < 0 {
		return errInvalidImage
	}

	p.slotCount = slotCount
	p.freeEnd = freeEnd
	p.recordBytes = recordBytes
	p.compactions = int(binary.BigEndian.Uint32(data[offCompactions:]))
	copy(p.buf, data)
	return nil
}

// Marshal 返回页映像的副本；映像逐字节确定，可交给 New 原样还原。
func (p *Page) Marshal() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]byte, p.pageSize)
	copy(out, p.buf)
	return out
}

// Stats 返回当前页内空间账目与累计整理次数。
func (p *Page) Stats() Stats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.statsLocked()
}

func (p *Page) statsLocked() Stats {
	dirEnd := p.headerSize + p.slotCount*p.slotSize
	live := 0
	for i := 0; i < p.slotCount; i++ {
		if p.slotLength(i) != 0 {
			live++
		}
	}
	slotBytes := p.slotCount * p.slotSize
	return Stats{
		PageSize:    p.pageSize,
		HeaderSize:  p.headerSize,
		SlotSize:    p.slotSize,
		SlotCount:   p.slotCount,
		LiveRecords: live,
		SlotBytes:   slotBytes,
		RecordBytes: p.recordBytes,
		FreeBytes:   p.pageSize - p.headerSize - slotBytes - p.recordBytes,
		FreeStart:   dirEnd,
		FreeEnd:     p.freeEnd,
		ContigFree:  p.freeEnd - dirEnd,
		Compactions: p.compactions,
	}
}
