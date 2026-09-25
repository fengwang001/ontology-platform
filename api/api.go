// Package api 是事件时间心跳水位推进对外的唯一入口：
// New / Feed / Heartbeat / Watermark / LateHeartbeats / SelfCheck。
// 依赖方向单向：api → hbs → wm。
package api

import (
	"errors"
	"fmt"

	"ontology/hbs"
	"ontology/wm"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrInvalidDelay = errors.New("heartbeat api: delay must be a positive integer")
	ErrEmptyKey     = errors.New("heartbeat api: data event key must not be empty")
	ErrNegativeTS   = errors.New("heartbeat api: event timestamp must not be negative")
)

// Engine 是一条事件时间流的对外句柄，状态只在进程内存。
type Engine struct {
	delay  int64
	stream *hbs.Stream
}

// New 以正整数 delay 创建引擎；delay 非正返回 ErrInvalidDelay，不建立任何状态。
func New(delay int64) (*Engine, error) {
	if delay <= 0 {
		return nil, ErrInvalidDelay
	}
	return &Engine{delay: delay, stream: hbs.New(delay)}, nil
}

// Feed 喂入数据事件 {Key, TS}，贡献 TS-delay。先全部校验再碰状态：失败不留痕。
func (e *Engine) Feed(key string, ts int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ts < 0 {
		return ErrNegativeTS
	}
	e.stream.Feed(ts)
	return nil
}

// Heartbeat 喂入心跳事件 {TS}，贡献 TS 本身；迟到心跳被接受且只计数。
func (e *Engine) Heartbeat(ts int64) error {
	if ts < 0 {
		return ErrNegativeTS
	}
	e.stream.Heartbeat(ts)
	return nil
}

func (e *Engine) Watermark() int64      { return e.stream.Watermark() }
func (e *Engine) LateHeartbeats() int64 { return e.stream.LateHeartbeats() }

type builtinEvent struct {
	hb  bool
	key string
	ts  int64
}

// SelfCheck 用内置八事件序列核验第二节四条不变量，并核验迟到判定
// 在 m=100/1000/10000 三档历史规模下为 O(1)（不回扫历史）；全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	steps := []builtinEvent{
		{false, "a", 5}, {true, "", 12}, {false, "b", 8}, {true, "", 9},
		{true, "", 20}, {false, "c", 14}, {true, "", 17}, {true, "", 25},
	}
	want := []struct{ wm, late int64 }{
		{2, 0}, {12, 0}, {12, 0}, {12, 1}, {20, 1}, {20, 1}, {20, 2}, {25, 2},
	}
	chk, err := New(e.delay)
	if err != nil {
		return err
	}
	batch, prev := wm.NegInfinity, wm.NegInfinity
	for i, ev := range steps {
		if ev.hb {
			if err := chk.Heartbeat(ev.ts); err != nil {
				return err
			}
			batch = max(batch, ev.ts) // 心跳贡献 TS 本身
		} else {
			if err := chk.Feed(ev.key, ev.ts); err != nil {
				return err
			}
			batch = max(batch, ev.ts-e.delay) // 数据贡献 TS-delay
		}
		gotWm, gotLate := chk.Watermark(), chk.LateHeartbeats()
		if gotWm != want[i].wm || gotLate != want[i].late {
			return fmt.Errorf("eight-step row %d: got wm=%d late=%d, want wm=%d late=%d",
				i+1, gotWm, gotLate, want[i].wm, want[i].late)
		}
		if gotWm != batch { // 不变量 1：与批量重算一致
			return fmt.Errorf("row %d: wm=%d diverges from batch max=%d", i+1, gotWm, batch)
		}
		if gotWm < prev { // 不变量 2：水位单调
			return fmt.Errorf("row %d: watermark retreated %d -> %d", i+1, prev, gotWm)
		}
		prev = gotWm
	}
	if err := chk.rejectionLeavesNoTrace(); err != nil { // 不变量 4
		return err
	}
	if !chk.stream.LateCheckBounded() {
		return errors.New("late judgment scans history: inspected count grows with m")
	}
	return nil
}

// rejectionLeavesNoTrace 验证三类拒绝可判定、互不相同、不改状态、之后仍可用。
func (e *Engine) rejectionLeavesNoTrace() error {
	if bad, err := New(0); bad != nil || !errors.Is(err, ErrInvalidDelay) {
		return fmt.Errorf("New(0): engine=%v err=%v", bad, err)
	}
	before := e.Watermark()
	calls := []func() error{
		func() error { return e.Feed("", 5) },
		func() error { return e.Feed("k", -1) },
		func() error { return e.Heartbeat(-1) },
	}
	wants := []error{ErrEmptyKey, ErrNegativeTS, ErrNegativeTS}
	for i, call := range calls {
		if err := call(); !errors.Is(err, wants[i]) {
			return fmt.Errorf("call %d: err=%v want %v", i, err, wants[i])
		}
	}
	if errors.Is(ErrEmptyKey, ErrNegativeTS) || errors.Is(ErrInvalidDelay, ErrEmptyKey) ||
		errors.Is(ErrInvalidDelay, ErrNegativeTS) {
		return errors.New("sentinel errors are not pairwise distinct")
	}
	if e.Watermark() != before {
		return fmt.Errorf("state changed by rejected events: %d -> %d", before, e.Watermark())
	}
	if err := e.Heartbeat(99); err != nil || e.Watermark() != 99 {
		return fmt.Errorf("engine unusable after rejections: err=%v wm=%d", err, e.Watermark())
	}
	return nil
}
