package intelhex

import "sync"

// Segment 是一段地址连续的半开内存区间 [Start, Start+len(Data))。
type Segment struct {
	Start uint32
	Data  []byte
}

// Loader 是线程安全的 Intel HEX 增量加载器。
type Loader struct {
	mu sync.Mutex

	lineNo    int    // 已接收的行序号（含被拒绝的行）
	finished  bool   // 是否已见到类型 01 的文件结束记录
	base      uint64 // 当前扩展地址（02: value<<4；04: value<<16）
	segments  []*seg // 互不重叠、严格分离、按地址升序的连续段
	startAddr uint32 // 最近一条 03/05 的 4 字节原始值
	startType byte   // 最近一条起始地址记录的类型（03 或 05）
	hasStart  bool
}

// seg 是一段内部表示的连续半开区间 [start, start+len(data))。
type seg struct {
	start uint64
	data  []byte
}

// New 创建一个空加载器。
func New() *Loader { return &Loader{} }

// AddLine 增量接收一行（不含换行符）。
// 任何被拒绝的行都不改变基址、映像、起始地址与文件结束状态。
func (l *Loader) AddLine(line string) (err error) {
	rec, perr := parseRecord(line)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.lineNo++
	no := l.lineNo
	if perr != nil {
		return &LineError{Line: no, Err: perr}
	}

	// 数据记录先做 64KiB 边界检查，再做重叠判定，
	// 二者均优先于“文件结束之后”的拒绝。
	var start, end uint64
	if rec.rtype == 0x00 {
		if rerr := checkDataRange(rec); rerr != nil {
			return &LineError{Line: no, Err: rerr}
		}
		start = l.base + uint64(rec.offset)
		end = start + uint64(rec.length)
		// LL=0 的数据记录不占字节、不进映像、不做重叠判定。
		if rec.length != 0 {
			if l.overlaps(start, end) {
				return &LineError{Line: no, Err: ErrOverlap}
			}
		}
	}

	// 前述全部检查通过后，文件结束记录之后再来任何行一律拒绝。
	// 因检查在前，拒绝行不会修改任何状态。
	if l.finished {
		return &LineError{Line: no, Err: ErrAfterEOF}
	}

	switch rec.rtype {
	case 0x00:
		if rec.length != 0 {
			l.put(start, end, rec.data)
		}
	case 0x01:
		l.finished = true
	case 0x02:
		l.base = uint64(uint16(rec.data[0])<<8|uint16(rec.data[1])) << 4
	case 0x04:
		l.base = uint64(uint16(rec.data[0])<<8|uint16(rec.data[1])) << 16
	case 0x03, 0x05:
		l.startAddr = uint32(rec.data[0])<<24 | uint32(rec.data[1])<<16 |
			uint32(rec.data[2])<<8 | uint32(rec.data[3])
		l.startType = rec.rtype
		l.hasStart = true
	}
	return nil
}

// Finish 要求已经见到类型 01 的文件结束记录。
func (l *Loader) Finish() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.finished {
		return ErrNotFinished
	}
	return nil
}

// Segments 返回按地址升序、首尾相接已合并的连续段（副本）。
func (l *Loader) Segments() []Segment {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []Segment
	for _, s := range l.segments {
		cp := make([]byte, len(s.data))
		copy(cp, s.data)
		out = append(out, Segment{Start: uint32(s.start), Data: cp})
	}
	return out
}

// StartAddress 返回最近一条类型 03/05 起始地址记录及其类型与存在标志。
func (l *Loader) StartAddress() (addr uint32, typ byte, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.startAddr, l.startType, l.hasStart
}

// overlaps 判定半开区间 [start, end) 是否与已有任何字节位置相交。
// 调用方须持有 mu。
func (l *Loader) overlaps(start, end uint64) bool {
	for _, s := range l.segments {
		segEnd := s.start + uint64(len(s.data))
		if start < segEnd && s.start < end {
			return true
		}
	}
	return false
}

// put 将数据写入映像。调用方须已确认区间不与已有数据重叠，
// 并持有 mu。段保持按地址升序、互不相交；插入时与前后首尾相接的
// 段直接合并，因此 Segments 只需做线性拼接。
func (l *Loader) put(start, end uint64, data []byte) {
	buf := make([]byte, len(data))
	copy(buf, data)

	idx := 0
	for idx < len(l.segments) && l.segments[idx].start < start {
		idx++
	}

	// 与前一段首尾相接：[prevEnd == start]，并入前段。
	if idx > 0 {
		prev := l.segments[idx-1]
		if prev.start+uint64(len(prev.data)) == start {
			prev.data = append(prev.data, buf...)
			// 若后一段恰好从 end 开始，继续并入并移除后段。
			if idx < len(l.segments) && end == l.segments[idx].start {
				prev.data = append(prev.data, l.segments[idx].data...)
				l.segments = append(l.segments[:idx], l.segments[idx+1:]...)
			}
			return
		}
	}

	// 与后一段首尾相接：[end == next.start]，前置合并后替换后段。
	if idx < len(l.segments) && end == l.segments[idx].start {
		next := l.segments[idx]
		merged := &seg{start: start, data: append(buf, next.data...)}
		l.segments[idx] = merged
		return
	}

	ns := &seg{start: start, data: buf}
	l.segments = append(l.segments, nil)
	copy(l.segments[idx+1:], l.segments[idx:])
	l.segments[idx] = ns
}
