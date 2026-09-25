package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/rec"
)

var ten = []rec.Rec{
	{Key: "a", Value: 1, TS: 1}, {Key: "b", Value: 10, TS: 2}, {Key: "a", Value: 2, TS: 4},
	{Key: "c", Value: 5, TS: 3}, {Key: "b", Value: 0, TS: 5, Del: true}, {Key: "a", Value: 0, TS: 6, Del: true},
	{Key: "c", Value: 0, TS: 7, Del: true}, {Key: "a", Value: 3, TS: 8}, {Key: "d", Value: 7, TS: 6}, {Key: "d", Value: 9, TS: 2},
}

func mustFeed(t *testing.T, a *API, rs []rec.Rec) {
	t.Helper()
	if err := a.Feed(rs); err != nil {
		t.Fatal(err)
	}
}

func mustCompact(t *testing.T, a *API, lo, hi int64) []rec.Rec {
	t.Helper()
	out, err := a.Compact(lo, hi)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// 不变量 1：任意 Feed 序列后 Compact 与朴素重算逐 Key 相同。
func TestCompactMatchesNaive(t *testing.T) {
	a := New(5)
	mustFeed(t, a, ten)
	want := []rec.Rec{{Key: "a", Value: 3, TS: 8}, {Key: "c", Value: 0, TS: 7, Del: true}, {Key: "d", Value: 7, TS: 6}}
	if got := mustCompact(t, a, 0, 10); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ten-record case: got %v want %v", got, want)
	}
	rng := rand.New(rand.NewSource(42))
	for _, m := range []int{10, 100, 1000} {
		for trial := 0; trial < 5; trial++ {
			var feeds []rec.Rec
			for i := 0; i < m; i++ { // 随机 Key 顺序、随机墓碑，同 Key TS 互异
				feeds = append(feeds, rec.Rec{Key: fmt.Sprintf("k%d", rng.Intn(m/2+1)), Value: i, TS: int64(i), Del: rng.Intn(4) == 0})
			}
			x := New(int64(rng.Intn(20)))
			mustFeed(t, x, feeds)
			lo, hi := int64(rng.Intn(m)), int64(m)
			if got, want := mustCompact(t, x, lo, hi), naive(feeds, lo, hi, x.r); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("m=%d trial=%d: got %v want %v", m, trial, got, want)
			}
		}
	}
}

// 不变量 2：结果中每个 Key 至多出现一次。
func TestNoDuplicateKeys(t *testing.T) {
	a := New(0)
	mustFeed(t, a, ten)
	seen := map[string]bool{}
	for _, r := range mustCompact(t, a, 0, 10) {
		if seen[r.Key] {
			t.Fatalf("duplicate key %q", r.Key)
		}
		seen[r.Key] = true
	}
}

// 不变量 3：墓碑被保留期丢弃后，更早的 put 不得复活。
func TestNoResurrection(t *testing.T) {
	a := New(5)
	mustFeed(t, a, ten)
	for _, r := range mustCompact(t, a, 0, 10) {
		if r.Key == "b" {
			t.Fatalf("b resurrected: %+v", r)
		}
	}
}

// 不变量 4：四类拒绝互不相同、状态不变、之后仍可正常使用。
func TestRejectStateIntact(t *testing.T) {
	a := New(5)
	mustFeed(t, a, ten)
	before := fmt.Sprint(a.View())
	if _, err := a.Compact(5, 5); !errors.Is(err, ErrBadWindow) {
		t.Fatalf("window: %v", err)
	}
	if _, err := New(-1).Compact(0, 1); !errors.Is(err, ErrBadRetention) {
		t.Fatalf("retention: %v", err)
	}
	if err := a.Feed([]rec.Rec{{Key: "", TS: 1}}); !errors.Is(err, rec.ErrEmptyKey) {
		t.Fatalf("empty key: %v", err)
	}
	if err := a.Feed([]rec.Rec{{Key: "z", TS: -1}}); !errors.Is(err, rec.ErrNegativeTS) {
		t.Fatalf("negative ts: %v", err)
	}
	errs := []error{ErrBadWindow, ErrBadRetention, rec.ErrEmptyKey, rec.ErrNegativeTS}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Fatalf("sentinel errors %d and %d not distinct", i, j)
			}
		}
	}
	if fmt.Sprint(a.View()) != before {
		t.Fatal("rejected ops changed state")
	}
	if got := mustCompact(t, a, 0, 10); len(got) != 3 {
		t.Fatalf("instance unusable after rejections: %v", got)
	}
}

// 并发：N 个 goroutine 对同一实例同一窗口 Compact，结果逐字段相同。
func TestConcurrentCompact(t *testing.T) {
	a := New(5)
	mustFeed(t, a, ten)
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint(mustCompact(t, a, 0, 10))
	const n = 32
	res := make([][]rec.Rec, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i], _ = a.Compact(0, 10)
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range res {
		if fmt.Sprint(res[i]) != want {
			t.Fatalf("goroutine %d: got %v want %v", i, res[i], want)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New(5).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
