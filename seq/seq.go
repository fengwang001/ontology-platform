// Package seq 对单个事件做纯函数判定：有序 / 相等 / 乱序三态与迟到量。
// 它不保存任何状态，也不依赖本工程其他包。
package seq

import "errors"

// ErrInvalidSeq 是序号非法（Seq <= 0）的哨兵错误。
var ErrInvalidSeq = errors.New("seq: invalid sequence number: must be >= 1")

// Kind 是单事件相对当前 MaxSeen 的三态判定。
type Kind uint8

const (
	// InOrder：此前没见过事件，或 Seq 严格大于 MaxSeen。
	InOrder Kind = iota
	// Equal：Seq == MaxSeen。语义上算有序，但不推进 MaxSeen，不算乱序。
	Equal
	// Late：Seq < MaxSeen，即乱序。
	Late
)

// Result 是 Classify 的判定结果。
// 仅当 Kind == Late 时 Lateness 有意义，值为 MaxSeen - Seq（恒正）。
type Result struct {
	Kind     Kind
	Lateness int64
}

// Classify 判定序号 v 相对当前标量 maxSeen 的三态。
// seen 表示此前是否已见过任何事件；false 时任意合法 v 都判 InOrder
// （MaxSeen 初始等价负无穷）。它不做、也无法做任何状态推进。
func Classify(v, maxSeen int64, seen bool) (Result, error) {
	if v <= 0 {
		return Result{}, ErrInvalidSeq
	}
	if !seen || v > maxSeen {
		return Result{Kind: InOrder}, nil
	}
	if v == maxSeen {
		return Result{Kind: Equal}, nil
	}
	return Result{Kind: Late, Lateness: maxSeen - v}, nil
}
