package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func ev(s int64, k string) api.Event { return api.Event{Seq: s, Key: k} }

// 第三节的九个事件（B/O 流向由各测试选择）。
var nine = []api.Event{ev(1, "a"), ev(3, "b"), ev(12, "b"), ev(5, "a"), ev(5, "a"), ev(7, "c"), ev(11, "c"), ev(9, "a"), ev(3, "b")}

func mustNew(t *testing.T, w int64) *api.API {
	t.Helper()
	a, err := api.New(w)
	if err != nil {
		t.Fatalf("New(%d): %v", w, err)
	}
	return a
}

func feed(t *testing.T, a *api.API, online bool, evs ...api.Event) {
	t.Helper()
	op := a.Backfill
	if online {
		op = a.Online
	}
	if err := op(evs); err != nil {
		t.Fatal(err)
	}
}

// 九步序列逐步核对 View 与 Seen；第 5、9 步为去重 no-op——不变量 1、3。
func TestNineStepSequence(t *testing.T) {
	online := []bool{false, false, true, false, true, false, true, false, true}
	views := [][3]int64{{1, 0, 0}, {1, 1, 0}, {1, 2, 0}, {2, 2, 0}, {2, 2, 0}, {2, 2, 1}, {2, 2, 2}, {3, 2, 2}, {3, 2, 2}}
	seens := []int{1, 2, 3, 4, 4, 5, 6, 7, 7}
	a := mustNew(t, 10)
	for i, e := range nine {
		feed(t, a, online[i], e)
		got := [3]int64{a.View()["a"], a.View()["b"], a.View()["c"]}
		if got != views[i] || a.Seen() != seens[i] {
			t.Fatalf("step %d: view=%v seen=%d", i+1, got, a.Seen())
		}
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 同一事件多重集、随机交错与随机切换点，终态必须相同——不变量 1、2。
func TestInterleaveInvariance(t *testing.T) {
	want := map[string]int64{"a": 3, "b": 2, "c": 2}
	for trial := 0; trial < 25; trial++ {
		a, done := mustNew(t, 10), false
		for i, p := range rand.New(rand.NewSource(int64(trial))).Perm(len(nine)) {
			e := nine[p]
			feed(t, a, e.Seq >= 10 || done || (i+trial)%2 == 0, e)
			if !done && i == trial%len(nine) {
				done = a.CompleteBackfill() == nil
			}
		}
		if !reflect.DeepEqual(a.View(), want) || a.Seen() != 7 {
			t.Fatalf("trial %d: view=%v seen=%d", trial, a.View(), a.Seen())
		}
	}
}

// 切换不得改变任何已正确计数，完成后回填被拒且不留痕——不变量 2、4。
func TestCutoverKeepsCounts(t *testing.T) {
	a := mustNew(t, 10)
	feed(t, a, false, ev(1, "a"), ev(3, "b"))
	feed(t, a, true, ev(12, "b"), ev(11, "c"))
	before := a.View()
	if err := a.CompleteBackfill(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.View(), before) {
		t.Fatalf("切换改变了计数: %v", a.View())
	}
	if err := a.Backfill([]api.Event{ev(2, "a")}); !errors.Is(err, api.ErrBackfillClosed) {
		t.Fatalf("完成后回填应报 ErrBackfillClosed，got %v", err)
	}
	if !reflect.DeepEqual(a.View(), before) {
		t.Fatalf("被拒回填留下了痕迹: %v", a.View())
	}
}

// 被拒操作状态零改变、错误可判定、之后可正常使用——不变量 4。
func TestRejectLeavesState(t *testing.T) {
	if _, err := api.New(-1); !errors.Is(err, api.ErrNegativeW) {
		t.Fatalf("负 W: got %v", err)
	}
	a := mustNew(t, 10)
	feed(t, a, false, ev(1, "a"))
	snap, seen := a.View(), a.Seen()
	ops := []func() error{
		func() error { return a.Backfill([]api.Event{ev(2, "x"), ev(10, "d")}) },
		func() error { return a.Online([]api.Event{ev(20, "")}) },
		func() error { return a.Backfill([]api.Event{ev(4, "")}) },
	}
	wants := []error{api.ErrSeqOutOfRange, api.ErrEmptyKey, api.ErrEmptyKey}
	for i, op := range ops {
		if err := op(); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: got %v want %v", i, err, wants[i])
		}
		if !reflect.DeepEqual(a.View(), snap) || a.Seen() != seen {
			t.Fatalf("case %d: 被拒后状态改变: %v %d", i, a.View(), a.Seen())
		}
	}
	feed(t, a, false, ev(2, "x")) // 拒绝后仍可正常使用
	if a.View()["x"] != 1 {
		t.Fatal("越界批中的合法事件被误应用或后续回填丢失")
	}
}

// N 个 goroutine 并发只读同一实例，View 逐字段相同、Seen 相同；无 sleep。
func TestConcurrentReaders(t *testing.T) {
	a := mustNew(t, 10)
	feed(t, a, false, ev(1, "a"), ev(3, "b"), ev(5, "a"))
	feed(t, a, true, ev(12, "b"), ev(11, "c"))
	if err := a.CompleteBackfill(); err != nil {
		t.Fatal(err)
	}
	want, wantSeen := a.View(), a.Seen()
	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if !reflect.DeepEqual(a.View(), want) || a.Seen() != wantSeen || a.SelfCheck() != nil {
				t.Error("读到的视图不一致")
			}
		}()
	}
	close(start)
	wg.Wait()
}
