// Package slot 表示句柄表中的单个槽位：值、当前代号、在用/空闲/耗尽状态。
package slot

// State 是槽位的生命周期状态。
type State uint8

const (
	// Free 表示槽位空闲且挂在空闲链表上。
	Free State = iota
	// Used 表示槽位被一个存活对象占用。
	Used
	// Exhausted 表示代号已达上限，槽位永不复用。
	Exhausted
)

// MaxGeneration 是 28 位代号字段的上限。
const MaxGeneration uint32 = 1<<28 - 1

// Slot 保存一个对象、它当前的代号以及槽位状态。
type Slot struct {
	value any
	gen   uint32
	state State
}

// New 以空闲、代号 0 创建槽位（首次分配时代号变为 1）。
func New() *Slot { return &Slot{} }

// State 返回槽位状态。
func (s *Slot) State() State { return s.state }

// Generation 返回槽位当前代号。
func (s *Slot) Generation() uint32 { return s.gen }

// Value 返回槽位中存放的值。
func (s *Slot) Value() any { return s.value }

// Alloc 把空闲槽位标记为在用并赋予代号，写入值。
func (s *Slot) Alloc(gen uint32, value any) {
	s.gen, s.value, s.state = gen, value, Used
}

// SetValue 更新在用槽位中的值。
func (s *Slot) SetValue(value any) { s.value = value }

// Release 释放在用槽位，清空值；代号达到 MaxGeneration 时标记为
// Exhausted（永不复用），否则代号加一并回到 Free。
func (s *Slot) Release() {
	s.value = nil
	if s.gen >= MaxGeneration {
		s.state = Exhausted
		return
	}
	s.gen++
	s.state = Free
}

// Force 仅供测试：把槽位代号推到指定值（不改变状态）。
func (s *Slot) Force(gen uint32) { s.gen = gen }
