package segment

import "ontology/zone"

// 段魔数与版本。
const (
	magic0, magic1, magic2, magic3 = 'O', 'S', 'E', 'G'
	version                        = 1
)

// statsOf 计算行组统计。min/max 只来自非空值；
// 全空时 HasValue 为假，min/max 是"无"而非零值。
func statsOf(vals []int64, nulls []bool) zone.Stats {
	st := zone.Stats{Rows: len(vals)}
	for i, v := range vals {
		if nulls[i] {
			st.Nulls++
			continue
		}
		if !st.HasValue {
			st.Min, st.Max, st.HasValue = v, v, true
			continue
		}
		if v < st.Min {
			st.Min = v
		}
		if v > st.Max {
			st.Max = v
		}
	}
	return st
}

// nullBitmap 生成空值位图，bit i 置位表示第 i 行为空。
func nullBitmap(nulls []bool) []byte {
	bm := make([]byte, (len(nulls)+7)/8)
	for i, n := range nulls {
		if n {
			bm[i/8] |= 1 << (uint(i) % 8)
		}
	}
	return bm
}

// bitmapHas 报告位图第 i 位是否置位。
func bitmapHas(bm []byte, i int) bool {
	return bm[i/8]&(1<<(uint(i)%8)) != 0
}

// assemble 把行组块组装成完整段：
// 魔数+版本+行组数+总行数+索引（各块长度）+ 块序列。
// 索引紧跟头部，截断会从尾部先破坏数据块，
// 因此行组级损坏能定位到具体行组与阶段。
func assemble(blocks [][]byte, totalRows int) (*Segment, error) {
	head := []byte{magic0, magic1, magic2, magic3, version}
	head = appendUvarint(head, uint64(len(blocks)))
	head = appendUvarint(head, uint64(totalRows))
	for _, blk := range blocks {
		head = appendUvarint(head, uint64(len(blk)))
	}
	data := head
	groups := make([]groupLoc, len(blocks))
	for i, blk := range blocks {
		groups[i] = groupLoc{start: len(data), length: len(blk)}
		data = append(data, blk...)
	}
	return &Segment{data: data, rows: totalRows, groups: groups}, nil
}

// groupLoc 记录行组块在段字节中的位置。
type groupLoc struct {
	start  int
	length int
}
