// Package cell 持有单个 Key 的值与版本，以及「快照校验」的判定逻辑。
package cell

// Cell 是单个 Key 的内部状态：当前值与版本号（版本不对外暴露）。
type Cell struct {
	Val int64
	Ver uint64
}

// Snap 是某时刻对 Cell 的只读快照。
type Snap struct {
	Val int64
	Ver uint64
}

// Snapshot 返回当前状态的快照。
func (c Cell) Snapshot() Snap { return Snap{Val: c.Val, Ver: c.Ver} }

// Unchanged 是快照校验的判定：当前版本是否仍等于快照版本。
func (c Cell) Unchanged(s Snap) bool { return c.Ver == s.Ver }

// Apply 返回施加 delta 后的新状态：值累加，版本恰好 +1。
func (c Cell) Apply(delta int64) Cell { return Cell{Val: c.Val + delta, Ver: c.Ver + 1} }
