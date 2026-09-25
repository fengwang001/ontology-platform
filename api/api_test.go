package api

import (
	"errors"
	"sync"
	"testing"

	"ontology/bucket"
)

// batch 把一组值重新逐个 floor 分桶计数，作为对照基准。
func batch(t *testing.T, width, anchor int, vals []int) map[int]int {
	t.Helper()
	m := map[int]int{}
	for _, v := range vals {
		m[bucket.Index(width, anchor, v)]++
	}
	return m
}

func feed(t *testing.T, width, anchor int, vals []int) *Histogram {
	t.Helper()
	h, err := New(width, anchor)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vals {
		h.Insert(v)
	}
	return h
}

var cases = []struct {
	name          string
	width, anchor int
	vals          []int
}{
	{"六步推导", 10, 0, []int{5, 12, 20, 10, -3, -15}},
	{"全负", 10, 0, []int{-1, -10, -11, -20, -9}},
	{"非零锚点", 4, 3, []int{3, 6, 7, 2, -5, 11}},
	{"稀疏远桶", 1, 0, []int{0, 10000, -10000, 5}},
}

func TestInsertMatchesBatch(t *testing.T) {
	for _, c := range cases {
		h := feed(t, c.width, c.anchor, c.vals)
		want := batch(t, c.width, c.anchor, c.vals)
		min, max, _ := h.Range()
		for k := min - 1; k <= max+1; k++ {
			if h.BucketCount(k) != want[k] {
				t.Errorf("%s 桶%d: got %d want %d", c.name, k, h.BucketCount(k), want[k])
			}
		}
	}
}

func TestTotalConservation(t *testing.T) {
	for _, c := range cases {
		h, _ := New(c.width, c.anchor)
		for i, v := range c.vals {
			h.Insert(v)
			if h.Total() != i+1 {
				t.Fatalf("%s 第%d步 Total=%d want %d", c.name, i+1, h.Total(), i+1)
			}
		}
	}
}

func TestRangeCompleteness(t *testing.T) {
	for _, c := range cases {
		h := feed(t, c.width, c.anchor, c.vals)
		min, max, ok := h.Range()
		if !ok {
			t.Fatalf("%s: 非空却报空范围", c.name)
		}
		if h.BucketCount(min-1) != 0 || h.BucketCount(max+1) != 0 {
			t.Errorf("%s: 范围外桶计数非零", c.name)
		}
		if h.BucketCount(min) == 0 || h.BucketCount(max) == 0 {
			t.Errorf("%s: 范围端点桶未被任何值落入", c.name)
		}
	}
	empty, _ := New(10, 0)
	if _, _, ok := empty.Range(); ok {
		t.Error("空直方图 Range 应报空")
	}
}

func TestNewInvalidWidth(t *testing.T) {
	for _, w := range []int{0, -1, -100} {
		h, err := New(w, 0)
		if !errors.Is(err, ErrNonPositiveWidth) {
			t.Errorf("width=%d: err=%v, 应可判定为 ErrNonPositiveWidth", w, err)
		}
		if h != nil {
			t.Errorf("width=%d: 被拒后产生了半成品状态", w)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	vals := []int{5, 12, 20, 10, -3, -15, 100, -100, 42}
	h := feed(t, 10, 0, vals)
	wantTotal := h.Total()
	wantMin, wantMax, _ := h.Range()
	wantCount := map[int]int{}
	for k := wantMin; k <= wantMax; k++ {
		wantCount[k] = h.BucketCount(k)
	}
	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if h.Total() != wantTotal {
				errs <- "Total 不一致"
			}
			for k := wantMin; k <= wantMax; k++ {
				if h.BucketCount(k) != wantCount[k] {
					errs <- "BucketCount 不一致"
				}
			}
			if err := h.SelfCheck(); err != nil {
				errs <- "SelfCheck 失败"
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
