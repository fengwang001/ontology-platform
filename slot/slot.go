// Package slot 定义句柄表中的单个槽位：存值、当前代号与在用/空闲状态。
package slot

// Slot 是句柄表中的一个槽位记录。
type Slot struct {
	Value     any    // 当前存放的对象值，空闲时为 nil
	Gen       uint32 // 当前代号，从 1 开始，只增不减、永不回绕
	InUse     bool   // 是否在用（已发出句柄）
	Exhausted bool   // 代号已耗尽，永久退役，不再复用
}
