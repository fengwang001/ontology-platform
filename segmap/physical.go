package segmap

import "sync"

// physRun 是一段引用计数相同的连续物理区间 [start, start+length)。
// count == 0 表示空闲。不变量：runs 按起点排序、首尾相接、互不重叠，
// 相邻 run 的 count 必须不同（相同则合并），因此物理布局表示唯一。
type physRun struct {
	start  int64
	length int64
	count  int64
}

// physical 是整池共享的物理空间。每个物理字节的引用计数恒等于
// 映射到它的逻辑字节数：一次映射长度为 n，则对应 n 个物理字节各 +1。
type physical struct {
	mu   sync.Mutex
	size int64
	data []byte
	runs []physRun
}

func newPhysical(size int64) *physical {
	return &physical{
		size: size,
		data: make([]byte, size),
		runs: []physRun{{length: size}},
	}
}

// allocate 取能容纳 length 字节的最低地址（first-fit），
// 新分配字节的引用计数置为 1。
func (p *physical) allocate(length int64) (int64, bool) {
	if length <= 0 {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.runs {
		r := &p.runs[i]
		if r.count == 0 && r.length >= length {
			start := r.start
			r.start += length
			r.length -= length
			allocated := physRun{start: start, length: length, count: 1}
			if r.length == 0 {
				p.runs = append(p.runs[:i], p.runs[i+1:]...)
			}
			p.runs = insertRun(p.runs, i, allocated)
			return start, true
		}
	}
	return 0, false
}

// retain 将 [start, start+length) 中每个字节的计数 +1（克隆共享）。
func (p *physical) retain(start, length int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.addCount(start, length, 1)
}

// release 将 [start, start+length) 中每个字节的计数 -1；
// 计数归零的字节立即回收（并入空闲空间，可被再次分配）。
func (p *physical) release(start, length int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.addCount(start, length, -1)
}

// usedBytes 返回计数非零的物理字节总数。
func (p *physical) usedBytes() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var used int64
	for _, r := range p.runs {
		if r.count > 0 {
			used += r.length
		}
	}
	return used
}

// addCount 在持有锁的前提下，把与 [start, end) 相交的各 run 调整计数。
// delta 只能为 +1 或 -1；调整后同计数相邻 run 被合并，保证表示唯一。
func (p *physical) addCount(start, length, delta int64) {
	end := start + length
	// 先在两个边界处劈开（可能落在任意 run 内部），再遍历调整。
	p.runs = splitRunAt(p.runs, 0, start)
	p.runs = splitRunAt(p.runs, 0, end)
	for i := range p.runs {
		if p.runs[i].start >= start && p.runs[i].start+p.runs[i].length <= end {
			p.runs[i].count += delta
		}
	}
	p.runs = coalesceRuns(p.runs)
}

// splitRunAt 若 at 落在某个 run 内部，则在 at 处劈开；
// at 与某 run 起点重合或位于空间外时为空操作。
func splitRunAt(runs []physRun, _ int, at int64) []physRun {
	if at < 0 {
		return runs
	}
	for i := range runs {
		r := runs[i]
		if r.start == at {
			return runs
		}
		if r.start < at && at < r.start+r.length {
			left := r
			right := physRun{start: at, length: r.length - (at - r.start), count: r.count}
			left.length = at - r.start
			runs = insertRun(runs, i+1, right)
			runs[i] = left
			return runs
		}
	}
	return runs
}

// coalesceRuns 合并计数相同（同为空闲或同为某计数值）的相邻 run。
func coalesceRuns(runs []physRun) []physRun {
	if len(runs) < 2 {
		return runs
	}
	out := runs[:1]
	for i := 1; i < len(runs); i++ {
		last := &out[len(out)-1]
		if last.start+last.length == runs[i].start && last.count == runs[i].count {
			last.length += runs[i].length
			continue
		}
		out = append(out, runs[i])
	}
	return out
}

func insertRun(runs []physRun, at int, r physRun) []physRun {
	runs = append(runs, physRun{})
	copy(runs[at+1:], runs[at:])
	runs[at] = r
	return runs
}
