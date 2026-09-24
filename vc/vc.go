// Package vc 提供向量时钟的比较、合法性校验与交付/重复判定。
// 它不依赖本工程其他任何包。
package vc

import "errors"

// 四类可判定错误中的两类：发送方非法、向量非法。
var (
	ErrSender = errors.New("vc: sender id out of range")
	ErrVector = errors.New("vc: illegal vector clock")
)

// Msg 是一条带发送方向量时钟的因果广播消息。
type Msg struct {
	From int
	V    []int64
}

// Validate 校验消息对 n 个发送方的系统是否合法：
// From 在 0..n-1；V 长度恰为 n；无负分量；自身序号 V[From] >= 1。
func Validate(m Msg, n int) error {
	if n <= 0 || m.From < 0 || m.From >= n {
		return ErrSender
	}
	if len(m.V) != n {
		return ErrVector
	}
	for _, x := range m.V {
		if x < 0 {
			return ErrVector
		}
	}
	if m.V[m.From] < 1 {
		return ErrVector
	}
	return nil
}

// Seen 判定消息是否已经交付过：自身序号不超过本地已交付计数。
func Seen(m Msg, local []int64) bool {
	return m.V[m.From] <= local[m.From]
}

// Deliverable 是规则给定的交付条件：
// m.V[j] == local[j]+1，且对所有 k != j 有 m.V[k] <= local[k]。
func Deliverable(m Msg, local []int64) bool {
	j := m.From
	if m.V[j] != local[j]+1 {
		return false
	}
	for k := range local {
		if k != j && m.V[k] > local[k] {
			return false
		}
	}
	return true
}

// Before 报告向量 a 是否逐分量 <= b 且至少一个分量严格小于（a 因果先于 b）。
// 两向量等长；空/等长零向量的比较按逐分量规则进行。
func Before(a, b []int64) bool {
	strict := false
	for i := range a {
		if a[i] > b[i] {
			return false
		}
		if a[i] < b[i] {
			strict = true
		}
	}
	return strict
}
