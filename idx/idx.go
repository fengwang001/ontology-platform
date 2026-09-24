// Package idx 实现稀疏位点索引：条目（int32 相对位点 + 物理位置）、
// 追加条目与 floor 二分查找。不依赖其他包。
package idx

// Entry 把相对位点映射到段内物理位置。
type Entry struct {
	Rel int32
	Pos int64
}

// Index 是只追加、按相对位点严格递增的条目序列。
type Index struct {
	entries []Entry
}

// Append 追加一个条目；调用方保证相对位点与物理位置严格递增。
func (ix *Index) Append(rel int32, pos int64) {
	ix.entries = append(ix.entries, Entry{Rel: rel, Pos: pos})
}

// Floor 返回相对位点 <= rel 的最后一个条目；checked 为本次二分
// 检查过的条目数。找不到时 ok 为 false。
func (ix *Index) Floor(rel int32) (e Entry, ok bool, checked int) {
	lo, hi := 0, len(ix.entries)
	for lo < hi {
		checked++
		mid := int(uint(lo+hi) >> 1)
		if ix.entries[mid].Rel <= rel {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Entry{}, false, checked
	}
	return ix.entries[lo-1], true, checked
}

// Entries 返回全部条目的副本。
func (ix *Index) Entries() []Entry {
	out := make([]Entry, len(ix.entries))
	copy(out, ix.entries)
	return out
}
