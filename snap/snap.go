// Package snap 定义快照位点语义：SP 的含/不含界定与增量衔接检查。
// 不依赖其他包。
package snap

import "errors"

// 可判定的哨兵错误，三者互不相同。
var (
	// ErrGap 位点不连续：ApplyIncremental 的 Pos != applied+1。
	ErrGap = errors.New("snap: position not contiguous")
	// ErrNegativeSP 快照位置非法：SP < 0。
	ErrNegativeSP = errors.New("snap: snapshot SP < 0")
	// ErrEmptyKey 事件 Key 非法：空串。
	ErrEmptyKey = errors.New("snap: empty event key")
)

// Event 变更流事件，语义 state[Key] += Delta，Pos 从 1 连续递增。
type Event struct {
	Pos   int64
	Key   string
	Delta int64
}

// Snapshot 全量快照：SP 为覆盖的最大位点（含），
// Table 等于位点 <= SP 的全部事件累加后的状态。
type Snapshot struct {
	SP    int64
	Table map[string]int64
}

// CheckSnapshot 校验快照合法性，不触碰任何状态。
func CheckSnapshot(s Snapshot) error {
	if s.SP < 0 {
		return ErrNegativeSP
	}
	return nil
}

// CheckEvent 校验事件能否接在 applied 之后：Key 非空且 Pos == applied+1。
func CheckEvent(applied int64, ev Event) error {
	if ev.Key == "" {
		return ErrEmptyKey
	}
	if ev.Pos != applied+1 {
		return ErrGap
	}
	return nil
}
