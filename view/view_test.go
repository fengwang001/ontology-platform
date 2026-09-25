package view

import (
	"errors"
	"fmt"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// 不变量 1：任意交错后 Read 等于提交日志重放。
func TestCommitLogReplay(t *testing.T) {
	type round struct {
		writes map[string]string
		commit bool // false = Abort
	}
	cases := map[string][]round{
		"只提交":     {{map[string]string{"a": "1"}, true}, {map[string]string{"b": "2"}, true}},
		"提交后回退":   {{map[string]string{"a": "1"}, true}, {map[string]string{"a": "x"}, false}},
		"覆盖同一key": {{map[string]string{"a": "1"}, true}, {map[string]string{"a": "2"}, true}},
		"空提交":     {{map[string]string{}, true}, {map[string]string{"a": "1"}, true}},
	}
	for name, rounds := range cases {
		t.Run(name, func(t *testing.T) {
			v, ref := New(), map[string]string{}
			var seq int64
			for _, r := range rounds {
				must(t, v.StartRefresh())
				for k, val := range r.writes {
					must(t, v.Stage(k, val))
				}
				if r.commit {
					must(t, v.Commit())
					for k, val := range r.writes {
						ref[k] = val
					}
					seq++
				} else {
					must(t, v.Abort())
				}
				gs, cells := v.Read()
				if gs != seq || len(cells) != len(ref) {
					t.Fatalf("seq=%d len=%d, want %d/%d", gs, len(cells), seq, len(ref))
				}
				for k, val := range ref {
					if cells[k] != val {
						t.Fatalf("cell %q=%q, want %q", k, cells[k], val)
					}
				}
			}
		})
	}
}

// 不变量 2：一次 Read 的 seq 与 cells 来自同一快照；pin 住的旧快照不被改写。
func TestReadConsistencyPinned(t *testing.T) {
	v := New()
	seq0, cells0 := v.Read()
	for i := 1; i <= 50; i++ {
		must(t, v.StartRefresh())
		must(t, v.Stage(fmt.Sprintf("k%d", i), "v"))
		must(t, v.Commit())
	}
	if seq0 != 0 || len(cells0) != 0 {
		t.Fatalf("pinned snapshot changed: seq=%d len=%d", seq0, len(cells0))
	}
	if seq, cells := v.Read(); seq != 50 || len(cells) != 50 || cells["k50"] != "v" {
		t.Fatalf("inconsistent pair: seq=%d len=%d", seq, len(cells))
	}
}

// 不变量 3：Stage 提交前不可见；Commit 原子可见且 Seq 恰 +1；Abort 完整回退。
func TestAtomicSwitchRollback(t *testing.T) {
	v := New()
	must(t, v.StartRefresh())
	must(t, v.Stage("a", "1"))
	if seq, cells := v.Read(); seq != 0 || len(cells) != 0 {
		t.Fatal("staged write visible before commit")
	}
	must(t, v.Commit())
	if seq, cells := v.Read(); seq != 1 || cells["a"] != "1" {
		t.Fatal("commit not atomically visible")
	}
	must(t, v.StartRefresh())
	must(t, v.Stage("a", "dirty"))
	must(t, v.Abort())
	if seq, cells := v.Read(); seq != 1 || cells["a"] != "1" {
		t.Fatal("abort did not roll back")
	}
}

// 不变量 4：被拒操作不改任何状态，错误互不相同，之后可正常使用。
func TestFailureNoTrace(t *testing.T) {
	v := New()
	for _, c := range []struct {
		name string
		op   func() error
	}{
		{"未refresh就Stage", func() error { return v.Stage("a", "1") }},
		{"未refresh就Commit", func() error { return v.Commit() }},
		{"未refresh就Abort", func() error { return v.Abort() }},
	} {
		if err := c.op(); !errors.Is(err, ErrNoRefresh) {
			t.Fatalf("%s: err=%v, want ErrNoRefresh", c.name, err)
		}
	}
	must(t, v.StartRefresh())
	if err := v.StartRefresh(); !errors.Is(err, ErrRefreshActive) {
		t.Fatalf("double StartRefresh: %v", err)
	}
	if err := v.Stage("", "1"); !errors.Is(err, ErrEmptyStageKey) {
		t.Fatalf("empty key: %v", err)
	}
	if ErrNoRefresh == ErrRefreshActive || ErrNoRefresh == ErrEmptyStageKey || ErrRefreshActive == ErrEmptyStageKey {
		t.Fatal("sentinel errors must be distinct")
	}
	if seq, cells := v.Read(); seq != 0 || len(cells) != 0 {
		t.Fatal("rejected ops changed state")
	}
	must(t, v.Stage("a", "1")) // 被拒后仍可正常使用
	must(t, v.Commit())
	if seq, _ := v.Read(); seq != 1 {
		t.Fatal("view unusable after rejected ops")
	}
}
