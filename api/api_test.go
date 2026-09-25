package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func intern(s string) error { _, err := Intern(s); return err }

// 第三节六步场景：{a:1,b:2,d:1}，驱逐 c。
func TestSixStep(t *testing.T) {
	must(t, New(3))
	for _, s := range []string{"a", "b", "c", "b"} {
		must(t, intern(s))
	}
	must(t, Release("c"))
	must(t, intern("d"))
	want := []Entry{{Value: "d", Refs: 1}, {Value: "b", Refs: 2}, {Value: "a", Refs: 1}}
	if got := Snapshot(); !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 不变量 1、2：多档规模随机序列 vs 朴素重放（计数与 MRU→LRU 顺序）。
func TestNaiveReplay(t *testing.T) {
	for _, tc := range [][3]int{{1, 500, 1}, {3, 2000, 2}, {8, 5000, 3}, {64, 20000, 4}} {
		max, ops := tc[0], tc[1]
		must(t, New(max))
		n := &naive{max: max, refs: map[string]int{}}
		r := rand.New(rand.NewSource(int64(tc[2])))
		keys := make([]string, 2*max)
		for i := range keys {
			keys[i] = fmt.Sprintf("k%d", i)
		}
		for i := 0; i < ops; i++ {
			s := keys[r.Intn(len(keys))]
			err, nerr := intern(s), n.intern(s)
			if r.Intn(2) == 1 {
				err, nerr = Release(s), n.release(s)
			}
			if (err == nil) != (nerr == nil) {
				t.Fatalf("max=%d 第 %d 步 err=%v nerr=%v", max, i, err, nerr)
			}
		}
		snap := Snapshot()
		if len(snap) != len(n.refs) || Len() > max {
			t.Fatalf("max=%d Len=%d 去重数=%d", max, Len(), len(n.refs))
		}
		ord := make([]string, 0, len(snap))
		for _, e := range snap {
			if n.refs[e.Value] != e.Refs {
				t.Fatalf("max=%d %s 计数=%d 朴素=%d", max, e.Value, e.Refs, n.refs[e.Value])
			}
			ord = append(ord, e.Value)
		}
		if !slices.Equal(ord, n.order) {
			t.Fatalf("max=%d 顺序 %v 朴素 %v", max, ord, n.order)
		}
	}
}

// 不变量 3：计数>0 的条目永不被驱逐；驱逐 LRU 端起第一个计数==0 的。
func TestEvictionProtection(t *testing.T) {
	must(t, New(3))
	for _, s := range []string{"a", "b", "c"} {
		must(t, intern(s))
	}
	if err := intern("d"); !errors.Is(err, ErrFull) {
		t.Fatalf("全池计数>0 时应报 ErrFull，got %v", err)
	}
	must(t, Release("a")) // a:0 且在 LRU 端
	must(t, intern("b"))  // b 移到 MRU
	must(t, intern("d"))
	want := []Entry{{Value: "d", Refs: 1}, {Value: "b", Refs: 2}, {Value: "c", Refs: 1}}
	if got := Snapshot(); !slices.Equal(got, want) {
		t.Fatalf("got %v want %v（应驱逐 a）", got, want)
	}
}

// 不变量 4：四类可判定错误互不相同，且被拒后状态不变。
func TestRejections(t *testing.T) {
	cases := []struct {
		name  string
		setup func()
		op    func() error
		want  error
	}{
		{"参数非法", func() { New(1) }, func() error { return New(0) }, ErrInvalidMaxEntries},
		{"池满", func() { New(1); Intern("a") }, func() error { return intern("b") }, ErrFull},
		{"未驻留", func() { New(1) }, func() error { return Release("ghost") }, ErrNotInterned},
		{"双重释放", func() { New(1); Intern("a"); Release("a") }, func() error { return Release("a") }, ErrDoubleRelease},
	}
	for _, tc := range cases {
		tc.setup()
		before := Snapshot()
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		if got := Snapshot(); !slices.Equal(got, before) {
			t.Fatalf("%s: 被拒后状态改变 %v -> %v", tc.name, before, got)
		}
	}
	errs := []error{ErrInvalidMaxEntries, ErrFull, ErrNotInterned, ErrDoubleRelease}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("%v 与 %v 不可区分", a, b)
			}
		}
	}
}

// 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	must(t, SelfCheck())
}
