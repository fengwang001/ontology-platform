// Package es 定义事件、快照与余额应用规则，不依赖其他包。
package es

import "errors"

// 可判定的哨兵错误，三类故障互不相同。
var (
	ErrBadSnapshot = errors.New("es: invalid snapshot") // Seq<0 或 Total<0
	ErrBadEvent    = errors.New("es: invalid event")    // Seq<=0，或追加乱序/重复
)

// Event 是一条余额变更事件，Seq 从 1 起连续递增。
type Event struct {
	Seq   int64
	Delta int64
}

// Snapshot 表示应用完 Seq<=快照.Seq 的全部事件之后的余额。
type Snapshot struct {
	Seq   int64
	Total int64
}

// Apply 应用一条事件：balance = max(0, balance+Delta)。
func Apply(balance int64, ev Event) int64 {
	b := balance + ev.Delta
	if b < 0 {
		return 0
	}
	return b
}

// ValidateSnapshot 校验快照：Seq>=0 且 Total>=0，否则 ErrBadSnapshot。
func ValidateSnapshot(s Snapshot) error {
	if s.Seq < 0 || s.Total < 0 {
		return ErrBadSnapshot
	}
	return nil
}

// ValidateEvent 校验事件本身：Seq 必须 >=1，否则 ErrBadEvent。
func ValidateEvent(ev Event) error {
	if ev.Seq <= 0 {
		return ErrBadEvent
	}
	return nil
}

// ValidateAppend 校验追加：ev.Seq 必须大于已追加的最后一条 lastSeq。
func ValidateAppend(lastSeq int64, ev Event) error {
	if err := ValidateEvent(ev); err != nil {
		return err
	}
	if ev.Seq <= lastSeq {
		return ErrBadEvent
	}
	return nil
}
