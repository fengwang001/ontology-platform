package consistency

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func testLogger(t *testing.T) func(string, ...any) {
	var mu sync.Mutex
	return func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		t.Logf(strings.TrimSpace(format), args...)
	}
}

func mustStore(t *testing.T, retain int, views ...string) *Store {
	t.Helper()
	s, err := NewStore(retain, views...)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s.SetLogger(testLogger(t))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func snapExpect(snap *Snapshot, want map[string]Value) error {
	if len(snap.Views) != len(want) {
		return fmt.Errorf("views=%d want=%d", len(snap.Views), len(want))
	}
	for _, vs := range snap.Views {
		w, ok := want[vs.View]
		if !ok {
			return fmt.Errorf("unexpected view %q", vs.View)
		}
		if vs.Value != w {
			return fmt.Errorf("view %q value=%v want=%v (versionTs=%d at=%d)",
				vs.View, vs.Value, w, vs.VersionTs, vs.At)
		}
	}
	return nil
}

func dumpState(s *Store) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	parts := make([]string, 0, len(s.order))
	for _, name := range s.order {
		vs := s.views[name]
		tss := make([]string, 0, len(vs.versions))
		for _, v := range vs.versions {
			tss = append(tss, fmt.Sprintf("%d=%v", v.ts, v.value))
		}
		parts = append(parts, fmt.Sprintf("%s(p=%d,[%s])", name, vs.progress, strings.Join(tss, ",")))
	}
	return strings.Join(parts, "|")
}

func TestNewStoreInvalid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		retain int
		views  []string
	}{
		{"retain zero", 0, []string{"a"}},
		{"retain negative", -2, []string{"a"}},
		{"no views", 3, nil},
		{"empty name", 3, []string{"a", ""}},
		{"duplicate", 3, []string{"a", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewStore(tc.retain, tc.views...); err == nil {
				t.Fatalf("expected error")
			} else if c := errClass(err); c != "invalid" {
				t.Fatalf("class=%s want invalid", c)
			}
		})
	}
}

func TestProgressMinAndBoundary(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 5, "a", "b")

	if _, ok := s.MinProgress(); ok {
		t.Fatal("MinProgress must be false before every view has a version")
	}
	if _, err := s.Read(ctx); errClass(err) != "not-ready" {
		t.Fatalf("empty store read class=%v want not-ready", err)
	}

	must(t, s.Apply(ctx, "a", 2, "a2"))
	must(t, s.Apply(ctx, "b", 2, "b2"))

	// 落后视图只靠心跳推进：minProgress=5，at=5 读到 a 的旧版本 a2。
	must(t, s.Apply(ctx, "a", 8, "a8"))
	must(t, s.Heartbeat(ctx, "a", 9))
	must(t, s.Apply(ctx, "b", 5, "b5"))
	if min, ok := s.MinProgress(); !ok || min != 5 {
		t.Fatalf("MinProgress=%d,%v want 5,true", min, ok)
	}

	// 边界：at == minProgress(5) 可读；at == 6 超过进度最小值，被第一关拒绝。
	snap, err := s.ReadAt(ctx, 5)
	if err != nil {
		t.Fatalf("read at 5: %v", err)
	}
	if err := snapExpect(snap, map[string]Value{"a": "a2", "b": "b5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadAt(ctx, 6); errClass(err) != "not-ready" {
		t.Fatalf("at 6 class=%v want not-ready", err)
	}

	// 心跳把 a 推进到 7（9 回退非法），再把 b 推进到 7，minProgress=7。
	must(t, s.Apply(ctx, "b", 7, "b7"))
	if min, ok := s.MinProgress(); !ok || min != 7 {
		t.Fatalf("MinProgress=%d want 7", min)
	}
	snap7, err := s.ReadAt(ctx, 7)
	if err != nil {
		t.Fatalf("read at 7: %v", err)
	}
	if err := snapExpect(snap7, map[string]Value{"a": "a2", "b": "b7"}); err != nil {
		t.Fatal(err)
	}

	if p, _ := s.Progress("a"); p != 9 {
		t.Fatalf("progress a=%d want 9", p)
	}
	if o, ok := s.Oldest("a"); !ok || o != 2 {
		t.Fatalf("oldest a=%d,%v want 2,true", o, ok)
	}
	if _, ok := s.Progress("ghost"); ok {
		t.Fatal("unknown view progress must be false")
	}

	// 另有视图从未产生版本：即使所有已有视图都就绪，整体仍未准备好。
	lagging := mustStore(t, 5, "a", "c")
	must(t, lagging.Apply(ctx, "a", 3, "a3"))
	must(t, lagging.Heartbeat(ctx, "c", 4)) // c 只有心跳
	if _, err := lagging.ReadAt(ctx, 3); errClass(err) != "not-ready" {
		t.Fatalf("view with heartbeat only class=%v want not-ready", err)
	}

	// 首版本即晚于时间点：窗口下界就是每个视图的第一个版本。
	firstVersion := mustStore(t, 5, "a", "b")
	must(t, firstVersion.Apply(ctx, "a", 3, "a3"))
	must(t, firstVersion.Apply(ctx, "b", 3, "b3"))
	if _, err := firstVersion.ReadAt(ctx, 2); errClass(err) != "too-old" {
		t.Fatalf("before first version class=%v want too-old", err)
	}
}

func TestEvictionTooOld(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 2, "a", "b")
	steps := []func() error{
		func() error { return s.Apply(ctx, "a", 1, "a1") },
		func() error { return s.Apply(ctx, "b", 1, "b1") },
		func() error { return s.Apply(ctx, "a", 2, "a2") },
		func() error { return s.Apply(ctx, "b", 2, "b2") },
		func() error { return s.Apply(ctx, "a", 3, "a3") }, // a 淘汰 ts=1
		func() error { return s.Apply(ctx, "b", 3, "b3") }, // b 淘汰 ts=1
		func() error { return s.Apply(ctx, "a", 4, "a4") }, // a 淘汰 ts=2
	}
	for _, op := range steps {
		must(t, op())
	}

	if o, _ := s.Oldest("a"); o != 3 {
		t.Fatalf("oldest a=%d want 3", o)
	}
	if o, _ := s.Oldest("b"); o != 2 {
		t.Fatalf("oldest b=%d want 2", o)
	}

	// minProgress=3；at=1、2 通过第一关，但 b 最旧版本为 2，被第二关拒绝。
	if _, err := s.ReadAt(ctx, 1); errClass(err) != "too-old" {
		t.Fatalf("at 1 class=%v want too-old", err)
	}
	if _, err := s.ReadAt(ctx, 2); errClass(err) != "too-old" {
		t.Fatalf("at 2 class=%v want too-old", err)
	}

	// 下界边界 at=3 可读，且视图值取不超过 3 的最大版本。
	snap, err := s.ReadAt(ctx, 3)
	if err != nil {
		t.Fatalf("at 3: %v", err)
	}
	if err := snapExpect(snap, map[string]Value{"a": "a3", "b": "b3"}); err != nil {
		t.Fatal(err)
	}

	// 推进并淘汰之后，只要 at=3 仍在窗口内，同点重读逐视图不变。
	must(t, s.Apply(ctx, "b", 4, "b4")) // b 淘汰 ts=2，窗口下界变为 3
	again, err := s.ReadAt(ctx, 3)
	if err != nil {
		t.Fatalf("reread at 3: %v", err)
	}
	if err := snapExpect(again, map[string]Value{"a": "a3", "b": "b3"}); err != nil {
		t.Fatal(err)
	}

	// 再淘汰，3 永久不可读。
	must(t, s.Apply(ctx, "a", 5, "a5"))
	must(t, s.Apply(ctx, "b", 5, "b5"))
	if _, err := s.ReadAt(ctx, 3); errClass(err) != "too-old" {
		t.Fatalf("after eviction at 3 class=%v want too-old", err)
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 3, "a", "b")

	reject := func(err error, want string) {
		t.Helper()
		if errClass(err) != want {
			t.Fatalf("class=%v want %s", err, want)
		}
	}

	// 每个阶段都在拒绝操作发生前拍下状态，拒绝后必须逐字节相同。
	runRejectionBatch := func(rejections func()) {
		before := dumpState(s)
		rejections()
		if after := dumpState(s); after != before {
			t.Fatalf("state changed after rejections:\nbefore=%s\nafter =%s", before, after)
		}
	}

	// 阶段一：空存储上的非法输入与尚未准备好。
	runRejectionBatch(func() {
		reject(s.Apply(ctx, "a", -1, "neg"), "invalid")  // 负时间戳
		reject(s.Apply(ctx, "ghost", 1, "x"), "invalid") // 未知视图
		reject(s.Apply(ctx, "a", 1, nil), "invalid")     // nil 值
		reject(s.Heartbeat(ctx, "ghost", 1), "invalid")  // 未知视图
		reject(s.Heartbeat(ctx, "a", -1), "invalid")     // 负时间戳
		if _, err := s.Read(ctx); errClass(err) != "not-ready" {
			t.Fatalf("empty read class=%v want not-ready", err)
		}
		if _, err := s.ReadAt(ctx, -1); errClass(err) != "invalid" {
			t.Fatalf("negative at class=%v want invalid", err)
		}
	})

	// 阶段二：已有进度后的不前进、超过最小进度与已淘汰太旧。
	must(t, s.Apply(ctx, "a", 10, "a10"))
	must(t, s.Apply(ctx, "b", 10, "b10"))
	must(t, s.Apply(ctx, "a", 12, "a12"))
	must(t, s.Apply(ctx, "b", 12, "b12"))
	must(t, s.Apply(ctx, "a", 13, "a13"))
	must(t, s.Apply(ctx, "a", 14, "a14")) // retain=3，a 已淘汰 ts=10
	runRejectionBatch(func() {
		reject(s.Apply(ctx, "a", 10, "dup"), "not-advancing") // 时间戳相等
		reject(s.Apply(ctx, "a", 9, "back"), "not-advancing") // 时间戳倒退
		reject(s.Apply(ctx, "a", 15, nil), "invalid")         // nil 值
		reject(s.Apply(ctx, "ghost", 15, "x"), "invalid")     // 未知视图
		reject(s.Apply(ctx, "a", -1, "neg"), "invalid")       // 负时间戳
		reject(s.Heartbeat(ctx, "b", 10), "not-advancing")    // 心跳不前进
		reject(s.Heartbeat(ctx, "ghost", 15), "invalid")      // 未知视图
		if _, err := s.ReadAt(ctx, -1); errClass(err) != "invalid" {
			t.Fatalf("negative at class=%v want invalid", err)
		}
		if _, err := s.ReadAt(ctx, 13); errClass(err) != "not-ready" { // b 进度为 12
			t.Fatalf("past min progress class=%v want not-ready", err)
		}
		if _, err := s.ReadAt(ctx, 10); errClass(err) != "too-old" {
			t.Fatalf("evicted at 10 class=%v want too-old", err)
		}
	})

	// 拒绝之后存储仍可正常工作，at=12 两视图都还在窗口内。
	snap, err := s.ReadAt(ctx, 12)
	if err != nil {
		t.Fatalf("read at 12 after rejections: %v", err)
	}
	if err := snapExpect(snap, map[string]Value{"a": "a12", "b": "b12"}); err != nil {
		t.Fatal(err)
	}
}

func TestErrorClassesDistinct(t *testing.T) {
	errs := []error{ErrInvalidArgument, ErrTimestampNotAdvancing, ErrNotReady, ErrTooOld}
	seen := map[string]bool{}
	for _, e := range errs {
		if seen[e.Error()] {
			t.Fatalf("duplicate error message: %q", e.Error())
		}
		seen[e.Error()] = true
		for _, other := range errs {
			if e == other {
				continue
			}
			if errors.Is(e, other) {
				t.Fatalf("%v unexpectedly Is %v", e, other)
			}
		}
	}
}
