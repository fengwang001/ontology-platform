// Package seq 只负责序号判定（非法 / 过期 / 命中 / 缺口）与丢失 Seq 记录，
// 不依赖本工程其他任何包。
package seq

import (
	"errors"
	"sort"
)

// 序号类哨兵错误，可被 errors.Is 判定。
var (
	// ErrExpired：s < next，该 Seq 已发出或已被跳过（丢失）。
	ErrExpired = errors.New("seq: Seq 过期")
	// ErrInvalid：s < 1。
	ErrInvalid = errors.New("seq: Seq 非法")
)

// Event 是上游投递、缓冲器按 Seq 升序发出的事件。
type Event struct {
	Seq   int64
	Value int
}

// Kind 是 Classify 的判定结果。
type Kind int

const (
	Invalid Kind = iota // s < 1
	Expired             // 1 <= s < next
	Hit                 // s == next
	Gap                 // s > next
)

// Classify 依据当前 next 判定 Seq 的四种情形。纯函数，无状态。
func Classify(s, next int64) Kind {
	if s < 1 {
		return Invalid
	}
	switch {
	case s < next:
		return Expired
	case s == next:
		return Hit
	default:
		return Gap
	}
}

// Lost 记录被超时 flush 判定为丢失的 Seq，永不重发。
// 非并发安全；并发由上层（api 包）统一加锁。
type Lost struct {
	seqs map[int64]struct{}
}

// NewLost 创建空的丢失记录。
func NewLost() *Lost {
	return &Lost{seqs: make(map[int64]struct{})}
}

// Add 记一条丢失 Seq。
func (l *Lost) Add(s int64) { l.seqs[s] = struct{}{} }

// Has 报告 s 是否已被记为丢失。
func (l *Lost) Has(s int64) bool {
	_, ok := l.seqs[s]
	return ok
}

// Len 返回丢失 Seq 的条数。
func (l *Lost) Len() int { return len(l.seqs) }

// All 返回升序拷贝，空时返回 nil；调用方修改不影响内部状态。
func (l *Lost) All() []int64 {
	if len(l.seqs) == 0 {
		return nil
	}
	out := make([]int64, 0, len(l.seqs))
	for s := range l.seqs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
