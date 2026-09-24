// Package handle 定义对外暴露的句柄：槽位号、代号、表号的编解码与零值判定。
// 句柄是 uint64，布局为 [tableID:16位][gen:20位][slot:28位]。
package handle

const (
	SlotBits  = 28
	GenBits   = 20
	TableBits = 16

	MaxSlot  = 1<<SlotBits - 1  // 槽位号上限（容量不得超过）
	MaxGen   = 1<<GenBits - 1   // 代号上限，触顶即退役，不回绕
	MaxTable = 1<<TableBits - 1 // 表号上限
)

// Handle 是指向某张表中某个槽位某一代对象的句柄。
// 零值（0）不是任何表发出的合法句柄：tableID 与 gen 都从 1 起步。
type Handle uint64

// Make 由表号、代号、槽位号编码出句柄。
func Make(tableID, gen, slot uint32) Handle {
	return Handle(uint64(tableID)<<(GenBits+SlotBits) |
		uint64(gen)<<SlotBits |
		uint64(slot))
}

// TableID 解出发表句柄的表号。
func (h Handle) TableID() uint32 { return uint32(uint64(h) >> (GenBits + SlotBits)) }

// Gen 解出句柄携带的代号。
func (h Handle) Gen() uint32 { return uint32(uint64(h)>>SlotBits) & MaxGen }

// Slot 解出句柄指向的槽位号。
func (h Handle) Slot() uint32 { return uint32(uint64(h)) & MaxSlot }

// IsZero 报告是否为零值句柄；零值句柄在任何表上都无效。
func (h Handle) IsZero() bool { return h == 0 }
