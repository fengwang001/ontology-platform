// Package slot 定义句柄表中的单个槽位：存值、当前代号、在用/空闲/耗尽状态。
package slot

// State 是槽位的生命周期状态。
type State uint8

const (
	Free      State = iota // 空闲，在空闲链表中，可被复用
	InUse                  // 在用，持有值与已发出的句柄
	Exhausted              // 代号耗尽，永久退役，不再复用
)

// Slot 是表中的一个槽位。零值即空闲槽位（代号 0，尚未发放过句柄）。
type Slot struct {
	Value any
	Gen   uint32 // 当前代号；发放句柄用递进前的值，故从 1 起才对外可见
	State State
	Next  int32 // 空闲链表后继槽位号，-1 表示链表尾；仅空闲时有意义
}

// Nil 是空闲链表的空指针值。
const Nil = -1
