// Package ev 定义批次事件类型与前置期望的判定，不依赖其他包。
package ev

import "errors"

// 可判定的哨兵错误。
var (
	ErrEmptyKey    = errors.New("ev: empty key")
	ErrExpectation = errors.New("ev: expectation not satisfied")
)

// Kind 是事件种类：upsert 或删除。
type Kind int

const (
	PutKind Kind = iota
	DelKind
)

// Event 是对某个键的一次 upsert 或删除，并带一个前置期望。
// Expect == nil 表示要求该键当前（批内累积状态里）不存在；
// Expect != nil 表示要求当前值恰好等于 *Expect。
type Event struct {
	Kind   Kind
	Key    string
	Val    string
	Expect *string
}

// Put 构造一个 upsert 事件。
func Put(key, val string, expect *string) Event {
	return Event{Kind: PutKind, Key: key, Val: val, Expect: expect}
}

// Del 构造一个删除事件。
func Del(key string, expect *string) Event {
	return Event{Kind: DelKind, Key: key, Expect: expect}
}

// CheckExpect 对照「键的当前状态」判定前置期望是否满足。
// val/ok 描述该键在批内累积状态里的值与存在性。
// 返回 nil 表示满足；否则返回包装了哨兵错误的可判定错误。
func CheckExpect(e Event, val string, ok bool) error {
	switch {
	case e.Expect == nil && ok:
		return &ExpectError{Key: e.Key, WantAbsent: true, Got: val, GotOK: ok}
	case e.Expect != nil && !ok:
		return &ExpectError{Key: e.Key, Want: *e.Expect}
	case e.Expect != nil && val != *e.Expect:
		return &ExpectError{Key: e.Key, Want: *e.Expect, Got: val, GotOK: ok}
	}
	return nil
}

// ExpectError 是前置期望失败的具体错误，Unwrap 得到 ErrExpectation。
type ExpectError struct {
	Key        string
	Want       string // 期望的值（WantAbsent 为 true 时无意义）
	WantAbsent bool   // 期望键不存在
	Got        string
	GotOK      bool
}

func (e *ExpectError) Error() string {
	if e.WantAbsent {
		return ErrExpectation.Error() + ": key " + e.Key + " must be absent but exists"
	}
	if !e.GotOK {
		return ErrExpectation.Error() + ": key " + e.Key + " must equal " + e.Want + " but is absent"
	}
	return ErrExpectation.Error() + ": key " + e.Key + " must equal " + e.Want + " but is " + e.Got
}

func (e *ExpectError) Unwrap() error { return ErrExpectation }
