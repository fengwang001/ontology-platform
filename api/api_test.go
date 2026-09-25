package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

func fill(a *api.API, n int) {
	for i := 0; i < n; i++ {
		_ = a.Put(fmt.Sprintf("k%05d", i), int64(i))
	}
}

func drain(t *testing.T, a *api.API) []api.Entry {
	var all []api.Entry
	cur := ""
	for {
		blk, nc, done, err := a.Resume(cur)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, blk...)
		if cur = nc; done {
			return all
		}
	}
}

// 不变量 1：各块按序拼接 == 快照时刻全量（多档规模 × 多档块大小）。
func TestChunkedConcatEqualsFull(t *testing.T) {
	for _, n := range []int{1, 5, 6, 7, 100, 1000} {
		for _, size := range []int{1, 2, 3, 10} {
			a, _ := api.New(size)
			fill(a, n)
			a.Snapshot()
			got := drain(t, a)
			if len(got) != n {
				t.Fatalf("n=%d size=%d: %d entries", n, size, len(got))
			}
			for i, e := range got {
				if want := fmt.Sprintf("k%05d", i); e.Key != want || e.Val != int64(i) {
					t.Fatalf("n=%d size=%d: [%d]=%v want %s=%d", n, size, i, e, want, i)
				}
			}
		}
	}
}

// 不变量 2：键两两不重复、并集 == 全量、字典序不倒退。
func TestNoDupNoGapOrdered(t *testing.T) {
	a, _ := api.New(3)
	fill(a, 100)
	a.Snapshot()
	seen := map[string]bool{}
	for i, e := range drain(t, a) {
		if seen[e.Key] || e.Key != fmt.Sprintf("k%05d", i) {
			t.Fatalf("dup/out-of-order at %q", e.Key)
		}
		seen[e.Key] = true
	}
	if len(seen) != 100 {
		t.Fatalf("union size %d != 100", len(seen))
	}
}

// 不变量 3：Snapshot 后的 Put/Del 不影响本轮导出值。
func TestPointInTime(t *testing.T) {
	a, _ := api.New(2)
	fill(a, 6)
	a.Snapshot()
	_ = a.Put("k00002", 99) // 覆盖已有键
	_ = a.Del("k00003")     // 删除已有键
	_ = a.Put("zz-new", 7)  // 新增键
	got := drain(t, a)
	if want := "[{k00000 0} {k00001 1} {k00002 2} {k00003 3} {k00004 4} {k00005 5}]"; fmt.Sprint(got) != want {
		t.Fatalf("post-snapshot write leaked: %v", got)
	}
}

// 不变量 4：被拒操作不改变实时状态与导出进度，且可继续正常使用。
func TestRejectedOpsLeaveState(t *testing.T) {
	a, _ := api.New(2)
	fill(a, 4)
	before := fmt.Sprint(a.View())
	fresh, _ := api.New(1)
	a.Snapshot()
	got := drain(t, a)
	_, errBad := api.New(0)
	_, _, _, errNo := fresh.Next("")
	_, _, _, errFin := a.Next("")
	rejects := map[error]error{api.ErrBadChunk: errBad, api.ErrNoSnapshot: errNo,
		api.ErrFinished: errFin, api.ErrEmptyKey: a.Put("", 1)}
	for want, err := range rejects {
		if !errors.Is(err, want) {
			t.Fatalf("want %v, got %v", want, err)
		}
	}
	if err := a.Del(""); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if fmt.Sprint(a.View()) != before || len(got) != 4 {
		t.Fatal("rejected ops changed state or broke export")
	}
}

// 四类哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	seen := map[error]bool{}
	for _, e := range []error{api.ErrBadChunk, api.ErrEmptyKey, api.ErrNoSnapshot, api.ErrFinished} {
		if seen[e] {
			t.Fatalf("duplicate sentinel %v", e)
		}
		seen[e] = true
	}
}

// 并发：导出与 N 个并发 Put 不同键的 goroutine 同时进行，结果仍等于快照时刻全量。
func TestConcurrentExportConsistent(t *testing.T) {
	a, _ := api.New(4)
	fill(a, 200)
	want := fmt.Sprint(a.View())
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				_ = a.Put(fmt.Sprintf("w%d-%03d", w, i), int64(i))
				_ = a.View()
				_ = api.SelfCheck()
			}
		}(w)
	}
	a.Snapshot()
	close(start)
	got := drain(t, a)
	wg.Wait()
	if fmt.Sprint(got) != want {
		t.Fatal("concurrent export != snapshot-time full")
	}
}
