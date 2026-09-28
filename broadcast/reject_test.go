package broadcast

import (
	"errors"
	"fmt"
	"testing"
)

// TestRejectionsLeaveNoTrace 对每类非法输入各拒绝一次，并校验全局版本、
// 各实例版本、缓冲区与已有命中均不发生变化（失败不留痕）。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	e, _ := newTestEngine(t, 2, 1)
	mustPublish(t, e, []Change{{Op: OpUpsert, Rule: Rule{ID: "r", Threshold: 10}}})
	if _, _, err := e.Deliver(0); err != nil { // 实例 0 -> 版本 1
		t.Fatal(err)
	}
	mustPublish(t, e, []Change{{Op: OpUpsert, Rule: Rule{ID: "r", Threshold: 11}}})
	if _, _, err := e.Send(4, 1); err != nil { // 实例 0 缓冲占位（标签 2）
		t.Fatal(err)
	}
	if _, _, err := e.Deliver(1); err != nil { // 实例 1 -> 版本 1
		t.Fatal(err)
	}
	if _, _, err := e.Deliver(1); err != nil { // 实例 1 -> 版本 2
		t.Fatal(err)
	}
	if _, _, err := e.Send(5, 100); err != nil { // 实例 1 立即处理产生命中
		t.Fatal(err)
	}

	snapshot := func() string {
		b0, _ := e.BufferLen(0)
		b1, _ := e.BufferLen(1)
		av0, _ := e.AppliedVersion(0)
		av1, _ := e.AppliedVersion(1)
		h0, _ := e.Hits(0)
		h1, _ := e.Hits(1)
		return fmt.Sprintf("gv=%d av=%d,%d buf=%d,%d hits=%d,%d",
			e.GlobalVersion(), av0, av1, b0, b1, len(h0), len(h1))
	}
	before := snapshot()

	type tc struct {
		name string
		want error
		call func() error
	}
	cases := []tc{
		{"empty publish", ErrInvalidPublish, func() error {
			_, err := e.Publish(nil)
			return err
		}},
		{"empty rule id upsert", ErrInvalidRule, func() error {
			_, err := e.Publish([]Change{{Op: OpUpsert, Rule: Rule{ID: ""}}})
			return err
		}},
		{"empty rule id delete", ErrInvalidRule, func() error {
			_, err := e.Publish([]Change{{Op: OpDelete, Rule: Rule{ID: ""}}})
			return err
		}},
		{"unknown op", ErrInvalidRule, func() error {
			_, err := e.Publish([]Change{{Op: Op(99), Rule: Rule{ID: "x"}}})
			return err
		}},
		{"mixed valid and invalid batch rejected atomically", ErrInvalidRule, func() error {
			_, err := e.Publish([]Change{
				{Op: OpUpsert, Rule: Rule{ID: "ok", Threshold: 1}},
				{Op: OpUpsert, Rule: Rule{ID: ""}},
			})
			return err
		}},
		{"deliver invalid instance negative", ErrInvalidInstance, func() error {
			_, _, err := e.Deliver(-1)
			return err
		}},
		{"deliver invalid instance too large", ErrInvalidInstance, func() error {
			_, _, err := e.Deliver(2)
			return err
		}},
		{"deliver out of range", ErrNoVersionToApply, func() error {
			_, _, err := e.Deliver(1) // 实例 1 已在版本 2（等于全局版本）
			return err
		}},
		{"negative key", ErrNegativeKey, func() error {
			_, _, err := e.Send(-1, 1)
			return err
		}},
		{"buffer full", ErrBufferFull, func() error {
			_, _, err := e.Send(6, 1) // 路由实例 0，缓冲已满
			return err
		}},
		{"query invalid instance", ErrInvalidInstance, func() error {
			_, err := e.Hits(7)
			return err
		}},
	}

	seen := map[error]bool{}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
		seen[c.want] = true
		if after := snapshot(); after != before {
			t.Errorf("%s changed state:\nbefore=%s\nafter =%s", c.name, before, after)
		}
	}
	for _, distinct := range []error{
		ErrInvalidPublish, ErrInvalidRule, ErrInvalidInstance,
		ErrNoVersionToApply, ErrNegativeKey, ErrBufferFull,
	} {
		if !seen[distinct] {
			t.Errorf("error category not exercised: %v", distinct)
		}
	}

	// 刷出占位数据后再发送：此前被缓冲满拒绝的发送不得残留任何影响。
	h1Before, _ := e.Hits(1)
	if _, _, err := e.Deliver(0); err != nil { // 实例 0 -> 版本 2，刷出占位数据
		t.Fatal(err)
	}
	if tag, _, err := e.Send(6, 100); err != nil || tag != 2 {
		t.Fatalf("send after drain: tag=%d err=%v", tag, err)
	}
	h0, _ := e.Hits(0)
	if len(h0) != 1 { // 占位 key=4 value=1 不命中；key=6 value=100 命中
		t.Fatalf("instance0 hits = %d, want 1 (%v)", len(h0), hitKeys(h0))
	}
	h1After, _ := e.Hits(1)
	if len(h1After) != len(h1Before) {
		t.Fatalf("instance1 hits changed unexpectedly: %d -> %d", len(h1Before), len(h1After))
	}
}

// TestNewEngineValidation 构造参数非法时失败。
func TestNewEngineValidation(t *testing.T) {
	for _, c := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
		if _, err := NewEngine(c[0], c[1], nil); !errors.Is(err, ErrInvalidInstance) {
			t.Errorf("NewEngine(%d,%d): err=%v want %v", c[0], c[1], err, ErrInvalidInstance)
		}
	}
}
