// Package wrap 实现 uint32 位点到 int64 单调值的展开算术与事件分类。
// 不依赖工程内其他包。
package wrap

import (
	"errors"
	"math"
)

// Event 是一次位点喂入的分类结果。
type Event int

const (
	First     Event = iota // 首个位点
	Forward                // 正常前进
	Wrap                   // 32 位计数回卷
	Duplicate              // 与上一条重复
	Rewind                 // 倒退异常：会被上层拒绝，不计入事件计数
)

func (e Event) String() string {
	switch e {
	case First:
		return "First"
	case Forward:
		return "Forward"
	case Wrap:
		return "Wrap"
	case Duplicate:
		return "Duplicate"
	case Rewind:
		return "Rewind"
	}
	return "Unknown"
}

// ErrOverflow 表示某步展开值将超过 int64 最大值。
var ErrOverflow = errors.New("wrap: unwrapped value overflows int64")

// space 是 2^32，即 uint32 计数器的模。
const space = 1 << 32

// Classify 判定 r 相对最近已接受位点 prev 的事件类别。
// threshold 是回卷判定阈值：prev-r > threshold 视为回卷，否则为倒退。
func Classify(prev, r, threshold uint32) Event {
	switch {
	case r == prev:
		return Duplicate
	case r > prev:
		return Forward
	case prev-r > threshold:
		return Wrap
	default:
		return Rewind
	}
}

// Unwrap 按事件类别把 r 展开为 64 位单调值；pu 是 prev 对应的展开值。
// Rewind 等不可展开的事件返回错误。
func Unwrap(pu int64, prev, r uint32, ev Event) (int64, error) {
	var delta int64
	switch ev {
	case First:
		return int64(r), nil
	case Duplicate:
		delta = 0
	case Forward:
		delta = int64(r) - int64(prev)
	case Wrap:
		delta = space - int64(prev) + int64(r)
	default:
		return 0, errors.New("wrap: cannot unwrap rejected event")
	}
	if pu > math.MaxInt64-delta {
		return 0, ErrOverflow
	}
	return pu + delta, nil
}
