package consistency

import (
	"context"
	"testing"
)

func TestContextAndDiagnostics(t *testing.T) {
	s := mustStore(t, 2, "a", "b")

	// nil ctx 合法。
	if err := s.Apply(nil, "a", 1, "a1"); err != nil {
		t.Fatalf("apply with nil ctx: %v", err)
	}

	// 已取消的 ctx：任何操作都直接返回，且按 context 错误失败、不留痕。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Apply(ctx, "b", 1, "b1"); err == nil {
		t.Fatal("apply with canceled ctx must fail")
	}
	if err := s.Heartbeat(ctx, "b", 1); err == nil {
		t.Fatal("heartbeat with canceled ctx must fail")
	}
	if _, err := s.ReadAt(ctx, 1); err == nil {
		t.Fatal("readAt with canceled ctx must fail")
	}
	if _, err := s.Read(ctx); err == nil {
		t.Fatal("read with canceled ctx must fail")
	}

	// SetLogger(nil) 必须安全恢复为静默。
	s.SetLogger(nil)
	if err := s.Heartbeat(nil, "a", 2); err != nil {
		t.Fatalf("heartbeat after logger reset: %v", err)
	}

	// 未知视图的诊断访问返回 false。
	if _, ok := s.Oldest("ghost"); ok {
		t.Fatal("Oldest on unknown view must be false")
	}
}

func TestReadWindowEmptyTooOld(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 2, "a", "b")

	// b 停在 10；a 连续应用到 12，retain=2 使 a 最旧版本变为 11。
	must(t, s.Apply(ctx, "b", 10, "b10"))
	must(t, s.Apply(ctx, "a", 10, "a10"))
	must(t, s.Apply(ctx, "a", 11, "a11"))
	must(t, s.Apply(ctx, "a", 12, "a12"))

	// 没有任何一个所有视图都已应用且仍保留的时间点：自动读取判为太旧。
	if _, err := s.Read(ctx); errClass(err) != "too-old" {
		t.Fatalf("Read class=%v want too-old", err)
	}

	// b 追上来后窗口恢复，自动读取返回最新一致点 11。
	must(t, s.Heartbeat(ctx, "b", 11))
	snap, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("Read after catch up: %v", err)
	}
	if snap.At != 11 {
		t.Fatalf("Read at=%d want 11", snap.At)
	}
	if err := snapExpect(snap, map[string]Value{"a": "a11", "b": "b10"}); err != nil {
		t.Fatal(err)
	}
}
