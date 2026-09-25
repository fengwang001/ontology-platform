// Package snap 定义快照位点语义：SP 的含/不含界定与增量衔接检查。
// 本包不依赖工程内其他包。
package snap

import "errors"

// 三类可判定的哨兵错误，互不相同。
var (
	// ErrBadSP 快照位点非法（SP < 0）。
	ErrBadSP = errors.New("snap: snapshot position must be >= 0")
	// ErrGap 增量位点不连续（Pos != applied+1，出现空洞或回退）。
	ErrGap = errors.New("snap: incremental position not contiguous")
	// ErrEmptyKey 事件 Key 为空串。
	ErrEmptyKey = errors.New("snap: event key is empty")
)

// Snapshot 是一张一致快照：Table 等于位点 <= SP 的全部事件累加后的状态。
// SP 是快照覆盖的最大位点（含），SP == 0 表示空快照（尚无任何事件）。
type Snapshot struct {
	SP    int64
	Table map[string]int64
}

// Event 是变更流中位点 Pos 投递的一条事件，语义为 state[Key] += Delta。
type Event struct {
	Pos   int64
	Key   string
	Delta int64
}

// CheckSnapshot 校验快照位点合法（SP >= 0）。
func CheckSnapshot(s Snapshot) error {
	if s.SP < 0 {
		return ErrBadSP
	}
	return nil
}

// CheckEvent 校验事件本身合法（Key 非空）。
func CheckEvent(ev Event) error {
	if ev.Key == "" {
		return ErrEmptyKey
	}
	return nil
}

// CheckContiguous 校验增量事件与已应用位点无缝衔接：
// ev.Pos 必须恰好等于 applied+1，否则出现空洞或重叠，拒绝。
func CheckContiguous(applied int64, ev Event) error {
	if ev.Pos != applied+1 {
		return ErrGap
	}
	return nil
}
