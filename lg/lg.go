// Package lg 实现 LogLog 的寄存器数组：Add 取 max、寄存器读取。
// 不依赖其他包；参数合法性由上层（api）校验，本包假设入参合法。
package lg

// Registers 是 m 个寄存器的数组，寄存器只增不减。
type Registers struct {
	reg []int
}

// New 返回 m 个全零寄存器。
func New(m int) *Registers {
	return &Registers{reg: make([]int, m)}
}

// Add 把 bucket 号寄存器更新为 max(旧值, z+1)。rank = z+1。
func (r *Registers) Add(bucket, z int) {
	if rank := z + 1; rank > r.reg[bucket] {
		r.reg[bucket] = rank
	}
}

// At 返回第 j 个寄存器的值。
func (r *Registers) At(j int) int {
	return r.reg[j]
}

// Len 返回寄存器个数 m。
func (r *Registers) Len() int {
	return len(r.reg)
}

// Snapshot 返回全部寄存器的副本。
func (r *Registers) Snapshot() []int {
	out := make([]int, len(r.reg))
	copy(out, r.reg)
	return out
}
