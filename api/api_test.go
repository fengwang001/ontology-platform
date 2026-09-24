package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/agg"
)

func TestSentinelErrors(t *testing.T) {
	if len(map[error]bool{ErrBadChunk: true, ErrBusy: true, ErrNotBuilding: true, ErrIncomplete: true}) != 4 {
		t.Fatal("api sentinel errors must be pairwise distinct")
	}
}

func TestSelfCheck(t *testing.T) {
	v, err := New([]string{"a", "b", "a"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	// SelfCheck 不得改动接收者状态。
	if !reflect.DeepEqual(v.View(), map[string]int{}) || v.Gen() != 0 {
		t.Fatal("SelfCheck must not mutate the receiver")
	}
}

// TestSelfCheckConcurrent 钉第六节：SelfCheck 可被多 goroutine 并发调用。
func TestSelfCheckConcurrent(t *testing.T) {
	v, _ := New([]string{"a", "b", "a"}, 2)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := v.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func rebuild(t *testing.T, v *View, src []string) {
	t.Helper()
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	for range src {
		if err := v.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := v.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReadsCommitBoundary 钉第六节：先 Commit 建好视图，N 个
// goroutine 并发只读 View，写入前首轮逐键相同；期间连续 Start+Step+Commit
// 重建，任一读到的 View 都必须是某个完整提交边界（视图恒等于朴素全量结果，
// 半成品计数会与其不同而被抓到）。全程不使用 sleep。
func TestConcurrentReadsCommitBoundary(t *testing.T) {
	for _, c := range []struct{ readers, rounds int }{{4, 20}, {8, 50}, {16, 30}} {
		src := []string{"a", "a", "b", "b", "c"}
		want := agg.Naive(src)
		v, err := New(src, 1)
		if err != nil {
			t.Fatal(err)
		}
		rebuild(t, v, src) // 先建好初始视图
		first := make(chan map[string]int, c.readers)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < c.readers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				first <- v.View() // 写入开始前：首轮结果必须逐键相同
				for {
					select {
					case <-stop:
						return
					default:
						cur := v.View()
						g := v.Gen()
						if g < 1 || !reflect.DeepEqual(cur, want) {
							t.Errorf("read off commit boundary: gen=%d view=%v", g, cur)
							return
						}
					}
				}
			}()
		}
		f0 := <-first
		for i := 1; i < c.readers; i++ {
			if !reflect.DeepEqual(<-first, f0) {
				t.Fatal("initial concurrent reads differ")
			}
		}
		for k := 0; k < c.rounds; k++ {
			rebuild(t, v, src)
		}
		close(stop)
		wg.Wait()
		if v.Gen() != c.rounds+1 || !reflect.DeepEqual(v.View(), want) {
			t.Fatalf("final gen=%d view=%v", v.Gen(), v.View())
		}
	}
}

// TestViewReturnsIndependentCopy 防止调用方通过返回的映射污染内部状态。
func TestViewReturnsIndependentCopy(t *testing.T) {
	v, _ := New([]string{"a", "b"}, 1)
	rebuild(t, v, []string{"a", "b"})
	got := v.View()
	got["a"] = 999
	if !reflect.DeepEqual(v.View(), map[string]int{"a": 1, "b": 1}) {
		t.Fatal("View must return an independent copy")
	}
}

// TestRejectedOpsViaAPI 确认对外层同样返回四类可判定哨兵错误。
func TestRejectedOpsViaAPI(t *testing.T) {
	if _, err := New([]string{"a"}, 0); !errors.Is(err, ErrBadChunk) {
		t.Fatalf("bad chunk: %v", err)
	}
	v, _ := New([]string{"a", "b"}, 2)
	if err := v.Step(); !errors.Is(err, ErrNotBuilding) {
		t.Fatalf("idle step: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	if err := v.Start(); !errors.Is(err, ErrBusy) {
		t.Fatalf("double start: %v", err)
	}
	if _, err := v.Commit(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("incomplete: %v", err)
	}
}
