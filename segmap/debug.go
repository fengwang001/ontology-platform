package segmap

import "fmt"

// ExtentView 是导出的区段视图：[LogicalStart, LogicalEnd) -> PhysicalStart。
type ExtentView struct {
	LogicalStart  int64
	LogicalEnd    int64
	PhysicalStart int64
}

// Extents 返回某文件当前映射的确定性快照（已排序、已合并）。
func (s *Store) Extents(name string) ([]ExtentView, bool) {
	f, ok := s.files[name]
	if !ok {
		return nil, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]ExtentView, 0, len(f.extents))
	for _, e := range f.extents {
		out = append(out, ExtentView{e.logicalStart, e.logicalEnd(), e.physicalStart})
	}
	return out, true
}

// Refcount 返回物理字节 at 的引用计数（测试/调试用）。
func (s *Store) Refcount(at int64) int64 {
	s.phys.mu.Lock()
	defer s.phys.mu.Unlock()
	for _, r := range s.phys.runs {
		if r.start <= at && at < r.start+r.length {
			return r.count
		}
	}
	return -1
}

// CheckInvariants 校验全部全局不变量，违例时返回描述信息：
//  1. 每个物理字节的引用计数 == 映射到它的逻辑字节数；
//  2. 已用物理字节 == 计数非零的物理字节数 == 全部已映射逻辑字节数；
//  3. 各区段有序、不重叠，且逻辑/物理双相接的相邻区段已被合并；
//  4. 物理 run 首尾覆盖整个空间、相邻 run 计数互不相同。
func (s *Store) CheckInvariants() error {
	s.phys.mu.Lock()
	runs := make([]physRun, len(s.phys.runs))
	copy(runs, s.phys.runs)
	size := s.phys.size
	s.phys.mu.Unlock()

	counts := make([]int64, size)
	var usedByRuns int64
	var totalRefcount int64
	var cursor int64
	for i, r := range runs {
		if r.start != cursor {
			return fmt.Errorf("physical runs not contiguous at run %d: want start %d got %d", i, cursor, r.start)
		}
		if r.length <= 0 {
			return fmt.Errorf("physical run %d has non-positive length", i)
		}
		if i > 0 && runs[i-1].count == r.count {
			return fmt.Errorf("adjacent physical runs %d,%d share count %d and were not merged", i-1, i, r.count)
		}
		if r.count > 0 {
			usedByRuns += r.length
			totalRefcount += r.length * r.count
			for b := r.start; b < r.start+r.length; b++ {
				counts[b] = r.count
			}
		}
		cursor += r.length
	}
	if cursor != size {
		return fmt.Errorf("physical runs cover %d bytes, want %d", cursor, size)
	}

	s.filesMu.Lock()
	names := make([]string, 0, len(s.files))
	for n := range s.files {
		names = append(names, n)
	}
	s.filesMu.Unlock()

	var mappedLogical int64
	for _, n := range names {
		f := s.files[n]
		f.mu.RLock()
		if len(f.extents) == 0 {
			f.mu.RUnlock()
			continue
		}
		var last *extent
		for i := range f.extents {
			e := &f.extents[i]
			if e.length <= 0 || e.physicalStart < 0 || e.physicalStart+e.length > size {
				f.mu.RUnlock()
				return fmt.Errorf("file %q extent %d out of physical bounds", n, i)
			}
			if e.logicalStart+e.length > f.length {
				f.mu.RUnlock()
				return fmt.Errorf("file %q extent %d exceeds file length %d", n, i, f.length)
			}
			if last != nil {
				if e.logicalStart < last.logicalEnd() {
					f.mu.RUnlock()
					return fmt.Errorf("file %q extents overlap", n)
				}
				logicallyAdjacent := last.logicalEnd() == e.logicalStart
				physicallyAdjacent := last.physicalStart+last.length == e.physicalStart
				if logicallyAdjacent && physicallyAdjacent {
					f.mu.RUnlock()
					return fmt.Errorf("file %q adjacent extents at %d should have been merged", n, e.logicalStart)
				}
			}
			for b := e.physicalStart; b < e.physicalStart+e.length; b++ {
				counts[b]--
			}
			mappedLogical += e.length
			last = e
		}
		f.mu.RUnlock()
	}

	for b, c := range counts {
		if c != 0 {
			return fmt.Errorf("physical byte %d refcount mismatch: run-count minus mappings = %d", b, c)
		}
	}
	// 口径：Σ(每物理字节计数) 恒等于映射逻辑字节总数；
	// 已用物理字节（计数非零字节数）只与非零 run 总长相等。
	if totalRefcount != mappedLogical {
		return fmt.Errorf("refcount-sum=%d but mapped logical bytes=%d（每个物理字节的计数须等于映射到它的逻辑字节数）",
			totalRefcount, mappedLogical)
	}
	if used := s.phys.usedBytes(); used != usedByRuns {
		return fmt.Errorf("used-bytes mismatch: nonzero-run=%d reported=%d（已用物理字节须等于计数非零字节数）",
			usedByRuns, used)
	}
	return nil
}
