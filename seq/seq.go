// Package seq 提供序号判定（过期/命中/缺口）与丢失 Seq 记录。不依赖其他包。
package seq

import (
	"errors"
	"sort"
)

// Event 是上游投递的事件，Seq 从 1 起、唯一、可能乱序到达。
type Event struct {
	Seq   int64
	Value int
}

// 可判定的哨兵错误。
var (
	ErrStale      = errors.New("seq: 序号过期")
	ErrInvalidSeq = errors.New("seq: 序号非法")
)

// Kind 是序号相对 next 的判定结果。
type Kind int

const (
	Stale Kind = iota // s < next，已发出或已被跳过
	Hit               // s == next，可立即发出
	Gap               // s > next，形成缺口需缓冲
)

// Validate 校验 Seq 合法性；s < 1 返回 ErrInvalidSeq。
func Validate(s int64) error {
	if s < 1 {
		return ErrInvalidSeq
	}
	return nil
}

// Classify 判定 s 相对 next 的类别。调用前须先通过 Validate。
func Classify(s, next int64) Kind {
	switch {
	case s < next:
		return Stale
	case s == next:
		return Hit
	default:
		return Gap
	}
}

// Lost 记录被超时 flush 记为丢失的 Seq，丢失的 Seq 永不重发。
type Lost struct {
	m map[int64]struct{}
}

// NewLost 返回空的丢失记录。
func NewLost() *Lost { return &Lost{m: make(map[int64]struct{})} }

// Add 把一个 Seq 记为丢失。
func (l *Lost) Add(s int64) { l.m[s] = struct{}{} }

// Has 报告 s 是否已被记为丢失。
func (l *Lost) Has(s int64) bool { _, ok := l.m[s]; return ok }

// List 按升序返回全部丢失 Seq 的副本；为空时返回 nil。
func (l *Lost) List() []int64 {
	var out []int64
	for s := range l.m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
