// Package api 是空闲超时水位的对外接口，依赖 idle。
package api

import (
	"errors"
	"fmt"
	"math"

	"ontology/idle"
	"ontology/wtm"
)

// 可判定哨兵错误，三者互不相同。
var (
	ErrNonPositiveParam = errors.New("api: delay/timeout 必须为正整数")
	ErrEmptyKey         = errors.New("api: 数据事件 Key 不能为空串")
	ErrNegativeTime     = errors.New("api: ts/pt 不能为负")
)

// Watermark 是一条事件流的水位句柄，并发安全。
type Watermark struct{ s *idle.Stream }

// New 创建实例；delay、timeout 非正时整体失败。
func New(delay, timeout int64) (*Watermark, error) {
	if delay <= 0 || timeout <= 0 {
		return nil, ErrNonPositiveParam
	}
	return &Watermark{s: idle.NewStream(delay, timeout)}, nil
}

// Feed 处理数据事件；所有校验先于任何状态修改，被拒则不留痕。
func (w *Watermark) Feed(key string, ts, pt int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ts < 0 || pt < 0 {
		return ErrNegativeTime
	}
	w.s.Feed(key, ts, pt)
	return nil
}

// Tick 推进处理时钟；pt 为负被拒且不留痕。
func (w *Watermark) Tick(pt int64) error {
	if pt < 0 {
		return ErrNegativeTime
	}
	w.s.Tick(pt)
	return nil
}

// Watermark 返回当前水位（只进不退）。
func (w *Watermark) Watermark() int64 { return w.s.Watermark() }

// Dropped 返回迟到丢弃计数。
func (w *Watermark) Dropped() int64 { return w.s.Dropped() }

type op struct {
	feed   bool
	key    string
	ts, pt int64
}

// replay 独立批量重算：不经过 idle.Stream，只用 wtm 纯函数取 max。
func replay(delay, timeout int64, ops []op) (int64, int64) {
	wm, dropped, lastPT := int64(math.MinInt64), int64(0), int64(0)
	has := false
	for _, o := range ops {
		if o.feed {
			lastPT, has = o.pt, true
			if wtm.Late(o.ts, delay, wm) {
				dropped++
			} else {
				wm = wtm.Advance(wm, o.ts-delay)
			}
		} else if wtm.Idle(o.pt, lastPT, timeout, has) {
			wm = wtm.Advance(wm, o.pt-delay)
		}
	}
	return wm, dropped
}

// SelfCheck 用内置序列核验四条不变量；只用自建实例，不碰接收者状态，可并发调用。
func (w *Watermark) SelfCheck() error {
	seqs := [][]op{
		{{true, "a", 10, 0}, {true, "b", 20, 1}, {false, "", 0, 10}, {true, "c", 20, 11},
			{false, "", 0, 12}, {false, "", 0, 30}, {true, "d", 25, 31}, {false, "", 0, 40}},
		{{false, "", 0, 5}, {true, "x", 100, 6}, {false, "", 0, 7}},     // 首 Tick 即空闲
		{{true, "e", 1000, 0}, {false, "", 0, 4}, {true, "f", 2000, 5}}, // 事件时间大、pt 间隔小
		{{true, "g", 1, 0}, {false, "", 0, 100}, {true, "h", 2, 101}},   // 事件时间小、pt 间隔大
	}
	for i, seq := range seqs {
		wm, err := New(3, 5)
		if err != nil {
			return err
		}
		prev := int64(math.MinInt64)
		for _, o := range seq { // 不变量 2：单调
			if o.feed {
				err = wm.Feed(o.key, o.ts, o.pt)
			} else {
				err = wm.Tick(o.pt)
			}
			if err != nil {
				return err
			}
			if wm.Watermark() < prev {
				return fmt.Errorf("selfcheck: 序列%d 水位回退", i)
			}
			prev = wm.Watermark()
		}
		bwm, bd := replay(3, 5, seq) // 不变量 1：与批量重算一致
		if wm.Watermark() != bwm || wm.Dropped() != bd {
			return fmt.Errorf("selfcheck: 序列%d 与批量重算不一致", i)
		}
	}
	// 不变量 3：空闲只看处理时间，推进用 pt-delay 而非事件时间。
	wm3, _ := New(3, 5)
	wm3.Feed("e", 1000, 0)
	wm3.Tick(4) // pt 间隔 4 < 5，不触发
	wm3.Tick(5) // 触发但 5-3=2 < 997，max 不动
	if wm3.Watermark() != 997 {
		return errors.New("selfcheck: 空闲判定/推进与事件时间挂钩")
	}
	wm4, _ := New(3, 5)
	wm4.Feed("g", 1, 0)
	wm4.Tick(100)
	if wm4.Watermark() != 97 { // 100-3，与事件时间 1 无关
		return errors.New("selfcheck: 空闲推进值不是 pt-delay")
	}
	// 不变量 4：失败不留痕，之后仍可正常使用。
	bad, _ := New(3, 5)
	bad.Feed("a", 10, 0)
	bw, bd := bad.Watermark(), bad.Dropped()
	_, e1 := New(0, 5)
	e2 := bad.Feed("", 1, 1)
	e3 := bad.Feed("k", -1, 1)
	e4 := bad.Tick(-1)
	if !errors.Is(e1, ErrNonPositiveParam) || !errors.Is(e2, ErrEmptyKey) ||
		!errors.Is(e3, ErrNegativeTime) || !errors.Is(e4, ErrNegativeTime) {
		return errors.New("selfcheck: 非法操作未给出可判定错误")
	}
	if bad.Watermark() != bw || bad.Dropped() != bd {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	if err := bad.Feed("b", 20, 1); err != nil || bad.Watermark() != 17 {
		return errors.New("selfcheck: 拒绝后无法继续正常使用")
	}
	return nil
}
