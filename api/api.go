// Package api 是对外门面：构造、喂入、查询与自检。依赖 eng。
package api

import (
	"errors"
	"fmt"
	"maps"

	"ontology/eng"
	"ontology/wrap"
)

// Event 与事件常量 re-export 自 wrap 包。
type Event = wrap.Event

const (
	First     = wrap.First
	Forward   = wrap.Forward
	Wrap      = wrap.Wrap
	Duplicate = wrap.Duplicate
)

// ErrThreshold 表示 New 的 threshold 不满足 1 <= threshold < 2^31。
var ErrThreshold = errors.New("api: threshold must satisfy 1 <= threshold < 2^31")

// Tracker 是位点展开跟踪器，并发安全。
type Tracker struct{ e *eng.Engine }

// New 构造跟踪器；threshold 非法时整体失败。
func New(threshold uint32) (*Tracker, error) {
	if threshold < 1 || threshold >= 1<<31 {
		return nil, ErrThreshold
	}
	return &Tracker{e: eng.New(threshold)}, nil
}

// Feed 喂入一个原始位点，返回分类事件；倒退与溢出被拒绝且不留痕。
func (t *Tracker) Feed(r uint32) (Event, error) { return t.e.Feed(r) }

// LastUnwrapped 返回最近接受位点的展开值；空态返回 eng.ErrEmpty。
func (t *Tracker) LastUnwrapped() (int64, error) { return t.e.LastUnwrapped() }

// LastRaw 返回最近接受的原始位点；空态返回 eng.ErrEmpty。
func (t *Tracker) LastRaw() (uint32, error) { return t.e.LastRaw() }

// Counts 返回事件计数的副本。
func (t *Tracker) Counts() map[Event]int { return t.e.Counts() }

// SelfCheck 在独立的内部实例上核验四条不变量，不触碰 t 的状态，可并发调用。
func (t *Tracker) SelfCheck() error {
	steps := []struct {
		r    uint32
		ev   Event
		want int64 // 仅接受步有效
		ok   bool
	}{
		{100, First, 100, true},
		{200, Forward, 200, true},
		{4294967200, Forward, 4294967200, true},
		{50, Wrap, 4294967346, true},
		{60, Forward, 4294967356, true},
		{40, 0, 0, false}, // 倒退被拒
		{4294967295, Forward, 8589934591, true},
		{5, Wrap, 8589934597, true},
	}
	// 推导取 threshold=2^31，但 New 的合法域是 [1, 2^31)，故用最大合法值
	// (1<<31)-1：各步 drop 为 4294967150/20/4294967290，分类结果与 2^31 完全相同。
	tr, err := New((1 << 31) - 1)
	if err != nil {
		return err
	}
	prevU := int64(-1)
	for i, s := range steps {
		ev, ferr := tr.Feed(s.r)
		if !s.ok { // 不变量 4：倒退必须被拒
			if !errors.Is(ferr, eng.ErrRewind) {
				return fmt.Errorf("selfcheck step %d: want rewind, got %v", i+1, ferr)
			}
			continue
		}
		u, _ := tr.LastUnwrapped()
		if ferr != nil || ev != s.ev || u != s.want || u <= prevU { // 不变量 1/2/3
			return fmt.Errorf("selfcheck step %d: got %v/%v/%d", i+1, ev, ferr, u)
		}
		prevU = u
	}
	// 不变量 4：拒绝后状态不变（prev=5，喂 3 必为倒退）
	u0, _ := tr.LastUnwrapped()
	c0 := tr.Counts()
	if _, err := tr.Feed(3); !errors.Is(err, eng.ErrRewind) {
		return fmt.Errorf("selfcheck: want rewind, got %v", err)
	}
	u1, _ := tr.LastUnwrapped()
	if u0 != u1 || !maps.Equal(c0, tr.Counts()) {
		return errors.New("selfcheck: rejected feed mutated state")
	}
	return nil
}
