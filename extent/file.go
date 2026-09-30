package extent

import "sync"

// extent 描述一段左闭右开的逻辑区间 [start, start+length)
// 到物理起点 phys 的线性映射。
type extent struct {
	start  int64
	length int64
	phys   int64
}

func (e extent) end() int64 { return e.start + e.length }

// piece 是被释放或共享的一段物理区间。
type piece struct {
	phys   int64
	length int64
}

// File 是一个逻辑文件：一组互不重叠、已合并的区段，加上长度上限。
// 所有修改操作在文件写锁与分配器锁下串行执行。
type File struct {
	vol   *Volume
	name  string
	limit int64 // 创建时给定的长度上限
	mu    sync.RWMutex
	len   int64 // 当前长度（截断可缩小）
	exts  []extent
}

// Length 返回文件当前长度。
func (f *File) Length() int64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.len
}

// Limit 返回文件长度上限。
func (f *File) Limit() int64 { return f.limit }

// Name 返回文件名。
func (f *File) Name() string { return f.name }

// unmapLocked 解除 [lo, hi) 的映射：部分重叠的区段被劈开，
// 未覆盖部分保留；返回被覆盖的物理片段供调用方释放。
// 调用方须持有文件写锁。
func (f *File) unmapLocked(lo, hi int64) []piece {
	var freed []piece
	kept := f.exts[:0]
	for _, e := range f.exts {
		if e.end() <= lo || e.start >= hi {
			kept = append(kept, e)
			continue
		}
		// 被覆盖部分 [cLo, cHi)。
		cLo, cHi := e.start, e.end()
		if cLo < lo {
			cLo = lo
		}
		if cHi > hi {
			cHi = hi
		}
		freed = append(freed, piece{phys: e.phys + (cLo - e.start), length: cHi - cLo})
		// 保留未覆盖的左段。
		if e.start < cLo {
			kept = append(kept, extent{start: e.start, length: cLo - e.start, phys: e.phys})
		}
		// 保留未覆盖的右段。
		if cHi < e.end() {
			kept = append(kept, extent{start: cHi, length: e.end() - cHi, phys: e.phys + (cHi - e.start)})
		}
	}
	f.exts = kept
	return freed
}

// insertLocked 插入一个新区段并与逻辑、物理都相接的相邻区段合并。
// 调用方须保证新区段不与现有区段重叠，且持有文件写锁。
func (f *File) insertLocked(e extent) {
	// 找到插入位置，保持按逻辑起点有序。
	i := 0
	for i < len(f.exts) && f.exts[i].start < e.start {
		i++
	}
	f.exts = append(f.exts, extent{})
	copy(f.exts[i+1:], f.exts[i:])
	f.exts[i] = e
	// 与左邻合并：逻辑与物理都相接。
	if i > 0 {
		left := f.exts[i-1]
		if left.end() == e.start && left.phys+left.length == e.phys {
			f.exts[i-1].length += e.length
			f.exts = append(f.exts[:i], f.exts[i+1:]...)
			i--
			e = f.exts[i]
		}
	}
	// 与右邻合并：逻辑与物理都相接。
	if i+1 < len(f.exts) {
		right := f.exts[i+1]
		if e.end() == right.start && e.phys+e.length == right.phys {
			f.exts[i].length += right.length
			f.exts = append(f.exts[:i+1], f.exts[i+2:]...)
		}
	}
}

// Extents 返回当前映射的只读快照（用于测试与调试）。
func (f *File) Extents() []extent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]extent, len(f.exts))
	copy(out, f.exts)
	return out
}
