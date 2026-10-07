package lsm

import "bytes"

// sortedIndex 维护一个非零层文件的有序索引。
// 文件按 (Smallest, Largest, ID) 升序排列。由于层不变量保证区间互不重叠
// （仅允许端点相接），该顺序下 Largest 同样单调不减——即使同一键上
// 存在单键文件 [k,k] 与长文件 [k,x] 共存的情形也成立，
// 因此所有查询都可以二分定位，开销为 O(log n + k)，
// 其中 n 为层内文件数，k 为结果数。
//
// 为了“可验证地证明”查询开销不随文件总数线性增长，
// 索引会统计每次查询执行的键比较次数，测试据此断言对数上界。
type sortedIndex struct {
	files []FileMeta
	cmps  int // 最近一次查询的键比较次数
}

// less 比较两个文件的 (Smallest, Largest, ID) 字典序。
func less(a, b FileMeta) bool {
	if c := bytes.Compare(a.Smallest, b.Smallest); c != 0 {
		return c < 0
	}
	if c := bytes.Compare(a.Largest, b.Largest); c != 0 {
		return c < 0
	}
	return a.ID < b.ID
}

// insert 将文件插入索引，保持有序。
func (ix *sortedIndex) insert(f FileMeta) {
	lo, hi := 0, len(ix.files)
	for lo < hi {
		mid := (lo + hi) / 2
		if less(ix.files[mid], f) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	ix.files = append(ix.files, FileMeta{})
	copy(ix.files[lo+1:], ix.files[lo:])
	ix.files[lo] = f
}

// remove 按 (Smallest, Largest, ID) 定位并删除文件；不存在时不做任何事。
func (ix *sortedIndex) remove(f FileMeta) {
	lo, hi := 0, len(ix.files)
	for lo < hi {
		mid := (lo + hi) / 2
		if less(ix.files[mid], f) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(ix.files) && ix.files[lo].ID == f.ID {
		copy(ix.files[lo:], ix.files[lo+1:])
		ix.files = ix.files[:len(ix.files)-1]
	}
}

// all 按序返回全部文件。
func (ix *sortedIndex) all() []FileMeta {
	out := make([]FileMeta, len(ix.files))
	copy(out, ix.files)
	return out
}

// lastQueryComparisons 返回最近一次查询的键比较次数，用于对数开销证明。
func (ix *sortedIndex) lastQueryComparisons() int {
	return ix.cmps
}

// overlap 返回所有与闭区间 [lo,hi] 有重叠（含端点相等）的文件，按键序排列。
// 开销 O(log n + k)：二分找到最右一个 Smallest <= hi 的文件，
// 然后向左扫描，遇到 Largest < lo 即停止（Largest 单调不减保证正确性）。
func (ix *sortedIndex) overlap(lo, hi []byte) []FileMeta {
	ix.cmps = 0
	// 二分：找最大的 i 使 files[i].Smallest <= hi；不存在则为 -1。
	left, right := -1, len(ix.files)
	for left+1 < right {
		mid := (left + right) / 2
		ix.cmps++
		if bytes.Compare(ix.files[mid].Smallest, hi) <= 0 {
			left = mid
		} else {
			right = mid
		}
	}
	var out []FileMeta
	for j := left; j >= 0; j-- {
		ix.cmps++
		if bytes.Compare(ix.files[j].Largest, lo) < 0 {
			break
		}
		out = append(out, ix.files[j])
	}
	// 恢复升序。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// withMin 返回所有 Smallest 等于 key 的文件，开销 O(log n + k)。
func (ix *sortedIndex) withMin(key []byte) []FileMeta {
	ix.cmps = 0
	start := ix.lowerBoundMin(key)
	var out []FileMeta
	for j := start; j < len(ix.files); j++ {
		ix.cmps++
		if bytes.Compare(ix.files[j].Smallest, key) != 0 {
			break
		}
		out = append(out, ix.files[j])
	}
	return out
}

// withMax 返回所有 Largest 等于 key 的文件，开销 O(log n + k)。
// 依赖 Largest 在 (Smallest, Largest, ID) 序下单调不减的性质。
func (ix *sortedIndex) withMax(key []byte) []FileMeta {
	ix.cmps = 0
	// 二分：第一个 Largest >= key 的位置。
	lo, hi := 0, len(ix.files)
	for lo < hi {
		mid := (lo + hi) / 2
		ix.cmps++
		if bytes.Compare(ix.files[mid].Largest, key) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	var out []FileMeta
	for j := lo; j < len(ix.files); j++ {
		ix.cmps++
		if bytes.Compare(ix.files[j].Largest, key) != 0 {
			break
		}
		out = append(out, ix.files[j])
	}
	return out
}

// lowerBoundMin 返回第一个 Smallest >= key 的下标。
func (ix *sortedIndex) lowerBoundMin(key []byte) int {
	lo, hi := 0, len(ix.files)
	for lo < hi {
		mid := (lo + hi) / 2
		ix.cmps++
		if bytes.Compare(ix.files[mid].Smallest, key) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// firstAfter 返回第一个 Smallest 严格大于 key 的文件；不存在时返回 false。
func (ix *sortedIndex) firstAfter(key []byte) (FileMeta, bool) {
	ix.cmps = 0
	i := ix.lowerBoundMin(key)
	// lowerBoundMin 找到的是 >= key 的第一个，需要跳过 == key 的。
	for i < len(ix.files) {
		ix.cmps++
		if bytes.Compare(ix.files[i].Smallest, key) > 0 {
			return ix.files[i], true
		}
		i++
	}
	return FileMeta{}, false
}
