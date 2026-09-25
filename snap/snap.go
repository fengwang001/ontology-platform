// Package snap 定义物化视图的不可变快照。
// 快照一旦创建绝不修改；需要变更时先 Clone 出可变副本，改完再固化为新快照。
package snap

// Snapshot 是一份不可变快照：Seq 单调递增，Cells 是该快照的全部单元。
type Snapshot struct {
	Seq   int64
	Cells map[string]string
}

// New 以给定 Seq 与 Cells 构造快照。调用方保证此后不再修改 cells。
func New(seq int64, cells map[string]string) *Snapshot {
	return &Snapshot{Seq: seq, Cells: cells}
}

// Clone 返回一份内容相同、可安全修改的深拷贝。
func (s *Snapshot) Clone() *Snapshot {
	c := make(map[string]string, len(s.Cells))
	for k, v := range s.Cells {
		c[k] = v
	}
	return &Snapshot{Seq: s.Seq, Cells: c}
}
