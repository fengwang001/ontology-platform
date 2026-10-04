// Package ledger 提供只追加的迁移历史账本。
package ledger

// Status 是账本行的状态。
type Status byte

// 行状态：S 成功、U 已回退、F 失败。
const (
	StatusSuccess Status = 'S'
	StatusUndone  Status = 'U'
	StatusFailed  Status = 'F'
)

// Row 是账本中的一行。rank 从 1 起严格递增、永不复用。
type Row struct {
	Rank   uint64
	Ver    uint32
	Sum    uint64
	Status Status
}

// Ledger 是只追加的行序列。Ledger 自身不加锁，并发串行化由调用方负责。
type Ledger struct {
	rows     []Row
	nextRank uint64
}

// New 返回空账本。
func New() *Ledger {
	return &Ledger{nextRank: 1}
}

// Append 追加一行并返回该行（含分配的 rank）。
func (l *Ledger) Append(ver uint32, sum uint64, st Status) Row {
	row := Row{Rank: l.nextRank, Ver: ver, Sum: sum, Status: st}
	l.nextRank++
	l.rows = append(l.rows, row)
	return row
}

// Rows 返回全部行的深拷贝（按 rank 升序）。
func (l *Ledger) Rows() []Row {
	out := make([]Row, len(l.rows))
	copy(out, l.rows)
	return out
}

// RemoveFailed 删除全部 F 行（rank 不回收），返回删除数。
func (l *Ledger) RemoveFailed() int {
	kept := l.rows[:0]
	removed := 0
	for _, row := range l.rows {
		if row.Status == StatusFailed {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	// 清空尾部，避免残留引用。
	for i := len(kept); i < len(l.rows); i++ {
		l.rows[i] = Row{}
	}
	l.rows = kept
	return removed
}

// MarkUndone 将指定 rank 的 S 行改为 U，返回是否成功。
func (l *Ledger) MarkUndone(rank uint64) bool {
	for i := range l.rows {
		if l.rows[i].Rank == rank && l.rows[i].Status == StatusSuccess {
			l.rows[i].Status = StatusUndone
			return true
		}
	}
	return false
}

// Applied 返回全部状态为 S 的行（按 rank 升序）。
func (l *Ledger) Applied() []Row {
	var out []Row
	for _, row := range l.rows {
		if row.Status == StatusSuccess {
			out = append(out, row)
		}
	}
	return out
}

// MaxAppliedVer 返回已应用集合 A 的最大 ver，空集为 0。
func (l *Ledger) MaxAppliedVer() uint32 {
	var max uint32
	for _, row := range l.rows {
		if row.Status == StatusSuccess && row.Ver > max {
			max = row.Ver
		}
	}
	return max
}

// FirstFailed 返回最小 rank 的 F 行。
func (l *Ledger) FirstFailed() (Row, bool) {
	for _, row := range l.rows {
		if row.Status == StatusFailed {
			return row, true
		}
	}
	return Row{}, false
}
