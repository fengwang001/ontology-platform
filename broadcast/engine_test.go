package broadcast

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func newTestEngine(t *testing.T, instances, capacity int) (*Engine, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	e, err := NewEngine(instances, capacity, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, &buf
}

func mustPublish(t *testing.T, e *Engine, changes []Change) int {
	t.Helper()
	v, err := e.Publish(changes)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return v
}

func hitKeys(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = fmt.Sprintf("v%d:k%d:%s", h.Version, h.Key, h.RuleID)
	}
	return out
}

// TestVersionSpanAndFlush 覆盖版本跨越：数据先于投递到达，跨多个版本后
// 投递必须严格逐版本应用，并按标签把对应数据刷出；缓冲按到达顺序处理，
// 同一数据的多条命中按规则标识排序。
func TestVersionSpanAndFlush(t *testing.T) {
	e, _ := newTestEngine(t, 2, 10)

	v1, err := e.Publish([]Change{
		{Op: OpUpsert, Rule: Rule{ID: "r-high", Threshold: 100}},
		{Op: OpUpsert, Rule: Rule{ID: "r-low", Threshold: 10}},
	})
	if err != nil || v1 != 1 {
		t.Fatalf("publish v1: v=%d err=%v", v1, err)
	}
	v2, err := e.Publish([]Change{{Op: OpUpsert, Rule: Rule{ID: "r-low", Threshold: 20}}})
	if err != nil || v2 != 2 {
		t.Fatalf("publish v2: v=%d err=%v", v2, err)
	}

	// key=4 路由实例 0；实例 0 尚未生效版本，数据以标签 2 缓冲。
	tag, inst, err := e.Send(4, 50)
	if err != nil || tag != 2 || inst != 0 {
		t.Fatalf("send 4: tag=%d inst=%d err=%v", tag, inst, err)
	}
	if n, _ := e.BufferLen(0); n != 1 {
		t.Fatalf("buffer len instance0 = %d, want 1", n)
	}
	tag2, inst2, err := e.Send(1, 50)
	if err != nil || tag2 != 2 || inst2 != 1 {
		t.Fatalf("send 1: tag=%d inst=%d err=%v", tag2, inst2, err)
	}

	// 实例 0 投递版本 1：标签 2 的数据必须继续缓冲，无处理结果。
	av, processed, err := e.Deliver(0)
	if err != nil || av != 1 || processed != 0 {
		t.Fatalf("deliver v1: av=%d processed=%d err=%v", av, processed, err)
	}
	if n, _ := e.BufferLen(0); n != 1 {
		t.Fatalf("buffer after v1 = %d, want 1", n)
	}
	if h, _ := e.Hits(0); len(h) != 0 {
		t.Fatalf("hits after v1 = %v, want none", h)
	}

	// 实例 0 投递版本 2：缓冲数据按标签 2 刷出，依据版本 2 的规则评估。
	av, processed, err = e.Deliver(0)
	if err != nil || av != 2 || processed != 1 {
		t.Fatalf("deliver v2: av=%d processed=%d err=%v", av, processed, err)
	}
	if n, _ := e.BufferLen(0); n != 0 {
		t.Fatalf("buffer after flush = %d, want 0", n)
	}
	hits, _ := e.Hits(0)
	got := hitKeys(hits)
	want := []string{"v2:k4:r-low"} // 50 < 100；50 >= 20
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hits = %v, want %v", got, want)
	}

	// 实例 1 跨越到版本 2：版本 1 无数据，版本 2 刷出标签 2 数据。
	if _, _, err := e.Deliver(1); err != nil {
		t.Fatalf("deliver inst1 v1: %v", err)
	}
	av, processed, err = e.Deliver(1)
	if err != nil || av != 2 || processed != 1 {
		t.Fatalf("deliver inst1 v2: av=%d processed=%d err=%v", av, processed, err)
	}

	// 已生效版本等于标签时立即处理；多条命中按规则标识排序。
	tag, inst, err = e.Send(2, 500)
	if err != nil || tag != 2 || inst != 0 {
		t.Fatalf("send immediate: tag=%d inst=%d err=%v", tag, inst, err)
	}
	hits, _ = e.Hits(0)
	got = hitKeys(hits)
	want = []string{"v2:k4:r-low", "v2:k2:r-high", "v2:k2:r-low"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hits after immediate send = %v, want %v", got, want)
	}
}

// TestBufferArrivalOrder 验证同版本缓冲数据严格按到达顺序刷出。
func TestBufferArrivalOrder(t *testing.T) {
	e, _ := newTestEngine(t, 1, 10)
	mustPublish(t, e, []Change{{Op: OpUpsert, Rule: Rule{ID: "r", Threshold: 0}}})
	for _, key := range []int{30, 10, 20} {
		if _, _, err := e.Send(key, 1); err != nil {
			t.Fatalf("send %d: %v", key, err)
		}
	}
	// 实例 applied=0，三条数据标签 1，全部缓冲，按到达顺序排列。
	if n, _ := e.BufferLen(0); n != 3 {
		t.Fatalf("buffer = %d, want 3", n)
	}
	if _, processed, err := e.Deliver(0); err != nil || processed != 3 {
		t.Fatalf("deliver: processed=%d err=%v", processed, err)
	}
	hits, _ := e.Hits(0)
	if len(hits) != 3 || hits[0].Key != 30 || hits[1].Key != 10 || hits[2].Key != 20 {
		t.Fatalf("flush order keys = %v, want 30,10,20", hitKeys(hits))
	}
}

// TestDeleteRule 验证删除规则：新版本下被删规则不再命中，旧标签数据
// 仍按旧版本快照处理；删除不存在的规则合法。
func TestDeleteRule(t *testing.T) {
	e, _ := newTestEngine(t, 1, 10)
	mustPublish(t, e, []Change{
		{Op: OpUpsert, Rule: Rule{ID: "a", Threshold: 1}},
		{Op: OpUpsert, Rule: Rule{ID: "b", Threshold: 1}},
	})
	if _, _, err := e.Deliver(0); err != nil {
		t.Fatal(err)
	}
	d1, _, _ := e.Send(10, 5) // 标签 1，立即处理
	mustPublish(t, e, []Change{{Op: OpDelete, Rule: Rule{ID: "b"}}})
	d2, _, _ := e.Send(12, 5) // 标签 2，缓冲（实例仍在版本 1）

	if d1 != 1 || d2 != 2 {
		t.Fatalf("tags = %d,%d want 1,2", d1, d2)
	}
	if n, _ := e.BufferLen(0); n != 1 {
		t.Fatalf("buffer = %d want 1", n)
	}
	if _, processed, err := e.Deliver(0); err != nil || processed != 1 {
		t.Fatalf("deliver v2: processed=%d err=%v", processed, err)
	}
	hits, _ := e.Hits(0)
	got := hitKeys(hits)
	want := []string{"v1:k10:a", "v1:k10:b", "v2:k12:a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hits = %v, want %v", got, want)
	}

	if _, err := e.Publish([]Change{{Op: OpDelete, Rule: Rule{ID: "ghost"}}}); err != nil {
		t.Fatalf("delete missing rule should be allowed: %v", err)
	}
}
