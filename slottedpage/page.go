// Package slottedpage 实现槽式记录页管理器：在固定字节大小的页内
// 插入、更新、删除变长记录，并在必要时就地整理碎片。
//
// 页内布局（字节偏移从 0 开始）：
//
//	[0, headerSize)            页头，前 12 字节由管理器占用（槽数、整理次数）
//	[headerSize, dirEnd)       槽目录，紧跟页头向高地址增长，每项 slotSize 字节
//	[dirEnd, lowPtr)           连续空闲区
//	[lowPtr, pageSize)         记录区，新记录紧挨当前最低记录向低地址放置
//
// 记录编号即槽下标，整理前后稳定不变。
package slottedpage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

const (
	// MinHeaderSize 是页头最小字节数：槽数 uint32 + 整理次数 uint64。
	MinHeaderSize = 12
	// MinSlotSize 是槽项最小字节数：记录偏移 uint32 + 记录长度 uint32。
	MinSlotSize = 8
)

// 可区分的拒绝原因，多因并存时按声明顺序只报第一个。
var (
	// ErrEmptyRecord 记录为空。
	ErrEmptyRecord = errors.New("slottedpage: empty record")
	// ErrRecordTooLarge 单条记录连同一个槽项超过空页容量。
	ErrRecordTooLarge = errors.New("slottedpage: record plus one slot exceeds empty page capacity")
	// ErrInvalidRecordID 编号越界或指向空槽。
	ErrInvalidRecordID = errors.New("slottedpage: record id out of range or empty slot")
	// ErrInsufficientSpace 可用空间不足（整理后仍不够）。
	ErrInsufficientSpace = errors.New("slottedpage: insufficient available space")
)

// Page 是槽式记录页，读写可并发调用，读者不会看到整理中的半移动状态。
type Page struct {
	mu          sync.RWMutex
	buf         []byte
	headerSize  int
	slotSize    int
	slotCount   int
	lowPtr      int // 当前最低记录的起始偏移；空页时等于页大小
	liveBytes   int // 存活记录字节总数
	compactions uint64
}

// New 创建空页。pageSize 为页总字节数，headerSize 为页头字节数，
// slotSize 为槽项字节数。
func New(pageSize, headerSize, slotSize int) (*Page, error) {
	if headerSize < MinHeaderSize {
		return nil, fmt.Errorf("slottedpage: header size %d < %d", headerSize, MinHeaderSize)
	}
	if slotSize < MinSlotSize {
		return nil, fmt.Errorf("slottedpage: slot size %d < %d", slotSize, MinSlotSize)
	}
	if headerSize+slotSize > pageSize {
		return nil, fmt.Errorf("slottedpage: header %d + slot %d exceeds page size %d",
			headerSize, slotSize, pageSize)
	}
	return &Page{
		buf:        make([]byte, pageSize),
		headerSize: headerSize,
		slotSize:   slotSize,
		lowPtr:     pageSize,
	}, nil
}

// Insert 插入记录，返回记录编号。优先复用编号最小的空槽，无空槽才新增槽项。
func (p *Page) Insert(rec []byte) (int, error) {
	if len(rec) == 0 {
		return -1, ErrEmptyRecord
	}
	if len(rec)+p.slotSize > len(p.buf)-p.headerSize {
		return -1, ErrRecordTooLarge
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	id := -1
	for i := 0; i < p.slotCount; i++ {
		if _, l := p.readSlot(i); l == 0 {
			id = i
			break
		}
	}
	need := len(rec)
	if id < 0 {
		need += p.slotSize
	}
	if p.availableLocked() < need {
		return -1, ErrInsufficientSpace
	}
	if p.contiguousLocked() < need {
		p.compactLocked()
	}
	off := p.lowPtr - len(rec)
	copy(p.buf[off:], rec)
	if id < 0 {
		id = p.slotCount
		p.slotCount++
	}
	p.writeSlot(id, off, len(rec))
	p.lowPtr = off
	p.liveBytes += len(rec)
	p.syncHeaderLocked()
	return id, nil
}

// Update 变长更新指定编号的记录。
func (p *Page) Update(id int, rec []byte) error {
	if len(rec) == 0 {
		return ErrEmptyRecord
	}
	if len(rec)+p.slotSize > len(p.buf)-p.headerSize {
		return ErrRecordTooLarge
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	off, old, err := p.lookupLocked(id)
	if err != nil {
		return err
	}
	delta := len(rec) - old
	if p.availableLocked() < delta {
		return ErrInsufficientSpace
	}
	switch {
	case delta <= 0:
		// 原地放得下：直接覆盖写。
		copy(p.buf[off:], rec)
		p.writeSlot(id, off, len(rec))
		p.liveBytes += delta
	case off == p.lowPtr && p.contiguousLocked() >= delta:
		// 本记录就是最低记录：向低地址就地扩展，无需整理。
		newOff := off - delta
		copy(p.buf[newOff:], rec)
		p.writeSlot(id, newOff, len(rec))
		p.lowPtr = newOff
		p.liveBytes += delta
	case p.contiguousLocked() >= len(rec):
		// 连续空闲区装得下整条记录：紧挨当前最低记录向前放置，无需整理。
		newOff := p.lowPtr - len(rec)
		copy(p.buf[newOff:], rec)
		p.writeSlot(id, newOff, len(rec))
		p.lowPtr = newOff
		p.liveBytes += delta
	default:
		// 可用够但连续空闲不够：整理时直接以新内容重排该记录。
		p.liveBytes += delta
		p.compactWithLocked(id, rec)
	}
	p.syncHeaderLocked()
	return nil
}

// Delete 删除记录：槽置空；若位于目录末尾，连同其前方连续空槽一并收回。
func (p *Page) Delete(id int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	off, l, err := p.lookupLocked(id)
	if err != nil {
		return err
	}
	p.writeSlot(id, 0, 0)
	p.liveBytes -= l
	for p.slotCount > 0 {
		if _, tail := p.readSlot(p.slotCount - 1); tail != 0 {
			break
		}
		p.slotCount--
	}
	if off == p.lowPtr {
		p.recomputeLowPtrLocked()
	}
	p.syncHeaderLocked()
	return nil
}

// Get 读取记录副本。
func (p *Page) Get(id int) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	off, l, err := p.lookupLocked(id)
	if err != nil {
		return nil, err
	}
	out := make([]byte, l)
	copy(out, p.buf[off:off+l])
	return out, nil
}

// Available 返回可用字节：页大小减页头、槽目录与存活记录字节。
func (p *Page) Available() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.availableLocked()
}

// ContiguousFree 返回槽目录与最低记录之间连续空闲区字节数。
func (p *Page) ContiguousFree() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.contiguousLocked()
}

// SlotCount 返回当前槽目录项数。
func (p *Page) SlotCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.slotCount
}

// Compactions 返回已发生的整理次数。
func (p *Page) Compactions() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.compactions
}

// Image 返回当前页映像副本；同一操作序列得到逐字节相同的映像。
func (p *Page) Image() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]byte, len(p.buf))
	copy(out, p.buf)
	return out
}

// Restore 从页映像还原出完全相同的页。headerSize 与 slotSize 须与
// 创建时一致；槽数与整理次数从映像页头中读回。
func Restore(image []byte, headerSize, slotSize int) (*Page, error) {
	if len(image) == 0 {
		return nil, errors.New("slottedpage: empty image")
	}
	p, err := New(len(image), headerSize, slotSize)
	if err != nil {
		return nil, err
	}
	slotCount := int(binary.LittleEndian.Uint32(image[0:]))
	if headerSize+slotCount*slotSize > len(image) {
		return nil, fmt.Errorf("slottedpage: slot directory of %d slots exceeds page", slotCount)
	}
	p.slotCount = slotCount
	p.compactions = binary.LittleEndian.Uint64(image[4:])
	copy(p.buf, image)

	dirEnd := p.dirEndLocked()
	low := len(image)
	for i := 0; i < slotCount; i++ {
		off, l := p.readSlot(i)
		if l == 0 {
			continue
		}
		if off < dirEnd || off+l > len(image) {
			return nil, fmt.Errorf("slottedpage: slot %d record [%d,%d) out of bounds", i, off, off+l)
		}
		p.liveBytes += l
		if off < low {
			low = off
		}
	}
	p.lowPtr = low
	return p, nil
}

// dirEndLocked 返回槽目录末尾偏移，即连续空闲区起点。
func (p *Page) dirEndLocked() int {
	return p.headerSize + p.slotCount*p.slotSize
}

// availableLocked = 页大小 - 页头 - 槽目录 - 存活记录字节。
func (p *Page) availableLocked() int {
	return len(p.buf) - p.dirEndLocked() - p.liveBytes
}

// contiguousLocked 返回槽目录与最低记录之间的连续空闲字节数。
func (p *Page) contiguousLocked() int {
	return p.lowPtr - p.dirEndLocked()
}

func (p *Page) lookupLocked(id int) (off, length int, err error) {
	if id < 0 || id >= p.slotCount {
		return 0, 0, ErrInvalidRecordID
	}
	off, length = p.readSlot(id)
	if length == 0 {
		return 0, 0, ErrInvalidRecordID
	}
	return off, length, nil
}

func (p *Page) readSlot(i int) (off, length int) {
	pos := p.headerSize + i*p.slotSize
	off = int(binary.LittleEndian.Uint32(p.buf[pos:]))
	length = int(binary.LittleEndian.Uint32(p.buf[pos+4:]))
	return off, length
}

func (p *Page) writeSlot(i, off, length int) {
	pos := p.headerSize + i*p.slotSize
	clear(p.buf[pos : pos+p.slotSize])
	binary.LittleEndian.PutUint32(p.buf[pos:], uint32(off))
	binary.LittleEndian.PutUint32(p.buf[pos+4:], uint32(length))
}

// compactLocked 就地整理碎片：存活记录按编号升序从页尾向低地址紧挨重排，
// 整理后编号越小的记录越靠页尾；槽内偏移同步改写，编号不变。
func (p *Page) compactLocked() {
	p.compactWithLocked(-1, nil)
}

// compactWithLocked 是 compactLocked 的底层实现；updID >= 0 表示把该编号
// 的记录以 updRec 的内容与长度参与重排（用于变长更新），调用方须已把
// 长度差计入 liveBytes。
func (p *Page) compactWithLocked(updID int, updRec []byte) {
	base := len(p.buf) - p.liveBytes
	staging := make([]byte, p.liveBytes)
	pos := len(p.buf)
	for i := 0; i < p.slotCount; i++ {
		off, l := p.readSlot(i)
		if i == updID {
			l = len(updRec)
		}
		if l == 0 {
			continue
		}
		pos -= l
		if i == updID {
			copy(staging[pos-base:pos-base+l], updRec)
		} else {
			copy(staging[pos-base:pos-base+l], p.buf[off:off+l])
		}
		p.writeSlot(i, pos, l)
	}
	copy(p.buf[base:], staging)
	p.lowPtr = base
	clear(p.buf[p.dirEndLocked():p.lowPtr])
	p.compactions++
}

func (p *Page) recomputeLowPtrLocked() {
	low := len(p.buf)
	for i := 0; i < p.slotCount; i++ {
		off, l := p.readSlot(i)
		if l > 0 && off < low {
			low = off
		}
	}
	p.lowPtr = low
}

// syncHeaderLocked 把槽数与整理次数写入页头，保证页映像自描述。
func (p *Page) syncHeaderLocked() {
	binary.LittleEndian.PutUint32(p.buf[0:], uint32(p.slotCount))
	binary.LittleEndian.PutUint64(p.buf[4:], p.compactions)
}
