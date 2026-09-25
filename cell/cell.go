// Package cell 持有单个 Key 的值与内部版本号，以及「快照校验」的判定逻辑。
// 不依赖其他包。
package cell

// Cell 是单个 Key 的值与版本号。零值即可用：val=0, ver=0。
type Cell struct {
	val int64
	ver uint64
}

// Snapshot 是某 Key 在某一时刻的 (val, ver) 只读快照。
type Snapshot struct {
	Val int64
	Ver uint64
}

// Snapshot 读取当前 (val, ver)。
func (c Cell) Snapshot() Snapshot { return Snapshot{Val: c.val, Ver: c.ver} }

// Unchanged 判定当前版本是否仍等于快照版本。
func (c Cell) Unchanged(s Snapshot) bool { return c.ver == s.Ver }

// Apply 应用增量并令版本恰 +1。只在成功提交路径、持锁状态下调用。
func (c *Cell) Apply(delta int64) { c.val += delta; c.ver++ }

// Val 返回当前值。
func (c Cell) Val() int64 { return c.val }
