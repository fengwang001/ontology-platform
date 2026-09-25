// Package view 在列式存储之上负责 rowID 分配、列子集投影与错误映射：
// 把 col 包的底层哨兵错误翻译成对外语义（不存在 / 已删 / 空列集）。
package view

import (
	"errors"

	"ontology/col"
)

// 对外三类可判定、互不相同的哨兵错误。
var (
	ErrNoSuchRow = errors.New("view: row id was never allocated")
	ErrDeleted   = errors.New("view: row has been deleted")
	ErrNoColumns = errors.New("view: project requires a non-empty column set")
)

// Col 重新导出三列标识，供上层按 A/B/C 选列。
type Col = col.Col

// A、B、C 为三列标识。
const (
	A = col.A
	B = col.B
	C = col.C
)

// View 持有底层列存与单调递增的 rowID 分配计数。
type View struct {
	st     *col.Store
	nextID int
}

// New 创建空视图。
func New() *View {
	return &View{st: col.New()}
}

// mapError 把 col 的底层错误映射为 view 的对外哨兵。
func mapError(err error) error {
	switch {
	case errors.Is(err, col.ErrNoSuchRow):
		return ErrNoSuchRow
	case errors.Is(err, col.ErrTombstone):
		return ErrDeleted
	default:
		return err
	}
}

// Insert 分配下一个单调递增 rowID，并令新行 append 到末尾槽。
func (v *View) Insert(x, y, z int64) int {
	id := v.nextID
	v.nextID++
	v.st.AppendRow(id, x, y, z)
	return id
}

// Update 改某行某列；失败在底层定位阶段即被整体拒绝。
func (v *View) Update(id int, k Col, val int64) error {
	return mapError(v.st.Update(id, k, val))
}

// Delete 置墓碑，不物理搬移。
func (v *View) Delete(id int) error {
	return mapError(v.st.Delete(id))
}

// Get 读某行三列；不存在或已删返回互不相同的哨兵错误。
func (v *View) Get(id int) (int64, int64, int64, error) {
	x, y, z, err := v.st.Get(id)
	if err != nil {
		return 0, 0, 0, mapError(err)
	}
	return x, y, z, nil
}

// Project 对非空列子集做投影：单次扫描存活槽，同一行的各列在同一次
// 内层循环里一起追加，因此各返回切片等长且逐元素列对齐，绝串行。
func (v *View) Project(sel []Col) (map[Col][]int64, error) {
	if len(sel) == 0 {
		return nil, ErrNoColumns
	}
	slots := v.st.AliveSlots()
	out := make(map[Col][]int64, len(sel))
	for _, k := range sel {
		out[k] = make([]int64, 0, len(slots))
	}
	for _, slot := range slots {
		for _, k := range sel {
			out[k] = append(out[k], v.st.At(slot, k))
		}
	}
	return out, nil
}

// Compact 物理移除死槽并重建 rowID→槽绑定。
func (v *View) Compact() { v.st.Compact() }

// Snapshot 透传底层逐格状态，供演示核对八步场景。
func (v *View) Snapshot() (a, b, c []int64, alive []bool, slotOf []int) {
	return v.st.Snapshot()
}

// VerifyLookupO1 透传底层「定位检查数不随行数增长」的布尔判定：多档规模下
// 检查槽数都必须恒为小常数 1；只回布尔，绝不暴露计数器数值。
func (v *View) VerifyLookupO1(sizes []int) bool {
	return col.VerifyLookupO1(sizes)
}
