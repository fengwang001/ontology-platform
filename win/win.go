// Package win 实现单窗口状态：agg、trg、state 迁移、触发判定、复活。
package win

// State 是窗口生命周期状态。
type State int

const (
	Active  State = iota // 正常累计并触发
	Purged               // 已清理，冻结保留，等待复活或 GC
	Revived              // 被一条迟到事件复活，永不再触发
)

func (s State) String() string {
	switch s {
	case Active:
		return "active"
	case Purged:
		return "purged"
	case Revived:
		return "revived"
	}
	return "unknown"
}

// Win 是单个窗口的聚合状态。
type Win struct {
	agg   int64
	trg   int64
	state State
}

// New 创建一个 active 窗口。
func New() *Win { return &Win{state: Active} }

// Add 累加 val；仅 active 窗口在 agg 从下往上越过 T 时触发一次。
// 返回本步是否产出触发器事件。revived 窗口照常累加但抑制触发（不变量 2）。
func (w *Win) Add(val, T int64) (fired bool) {
	old := w.agg
	w.agg += val
	if w.state == Active && old < T && w.agg >= T {
		w.trg++
		return true
	}
	return false
}

// Purge 冻结窗口：agg/trg 保留，state → purged。
func (w *Win) Purge() {
	if w.state == Active {
		w.state = Purged
	}
}

// Revive 复活：agg 只取这一条迟到事件的 val，丢弃冻结历史（不变量 1）。
func (w *Win) Revive(val int64) {
	w.agg = val
	w.state = Revived
}

func (w *Win) Agg() int64   { return w.agg }
func (w *Win) Trg() int64   { return w.trg }
func (w *Win) State() State { return w.state }
