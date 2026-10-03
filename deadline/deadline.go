// Package deadline 由配置与当前尝试推导各类到期时刻并用最小堆取最早到期者。
package deadline

import "errors"

// ErrConfig 表示到期配置非法。
var ErrConfig = errors.New("deadline: invalid config")

// Kind 标识一类到期事件。
type Kind uint8

const (
	SC  Kind = iota // 总时限，同刻优先级最高
	S2C             // 单次尝试执行时限
	HB              // 心跳间隔
	S2S             // 排队到开始时限，优先级最低
)

// Config 为四类时限，0 表示不设。
type Config struct {
	S2S int64
	S2C int64
	HB  int64
	SC  int64
}

// Validate 校验取值范围（1..1e9，且 s2c 与 sc 至少一个非零）。
func (c Config) Validate() error {
	for _, d := range []int64{c.S2S, c.S2C, c.HB, c.SC} {
		if d < 0 || d > 1_000_000_000 {
			return ErrConfig
		}
	}
	if c.S2C == 0 && c.SC == 0 {
		return ErrConfig
	}
	return nil
}

// Item 是堆中的一个到期项。
type Item struct {
	At   int64
	Kind Kind
}
