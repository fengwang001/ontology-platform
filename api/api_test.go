package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/bucket"
)

var seqs = []struct {
	name   string
	width  int
	anchor int
	vals   []int
}{
	{"six-step w10 a0", 10, 0, []int{5, 12, 20, 10, -3, -15}},
	{"negatives only", 10, 0, []int{-1, -11, -21, -5}},
	{"shifted anchor", 7, 3, []int{3, 9, 10, 2, -4, 17, 100}},
	{"width one", 1, 0, []int{-3, -2, -1, 0, 1, 2, 2, 2}},
	{"big width", 1000, -50, []int{-5000, 0, 4999, -51, 950}},
}

func fill(t *testing.T, width, anchor int, vals []int) *api.Histogram {
	h, err := api.New(width, anchor)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", width, anchor, err)
	}
	for _, v := range vals {
		h.Insert(v)
	}
	return h
}

func batchOf(width, anchor int, vals []int) map[int]int {
	m := map[int]int{}
	for _, v := range vals {
		m[bucket.Number(v, anchor, width)]++
	}
	return m
}

// 不变量 1：与批量重算逐桶一致。
func TestBatchEquivalence(t *testing.T) {
	for _, s := range seqs {
		t.Run(s.name, func(t *testing.T) {
			h := fill(t, s.width, s.anchor, s.vals)
			batch := batchOf(s.width, s.anchor, s.vals)
			lo, hi, ok := h.Range()
			if !ok {
				t.Fatal("range empty after inserts")
			}
			for k := lo - 2; k <= hi+2; k++ {
				if got := h.BucketCount(k); got != batch[k] {
					t.Errorf("BucketCount(%d)=%d want %d", k, got, batch[k])
				}
			}
		})
	}
}

// 不变量 2：计数守恒。
func TestTotalConservation(t *testing.T) {
	for _, s := range seqs {
		t.Run(s.name, func(t *testing.T) {
			if got := fill(t, s.width, s.anchor, s.vals).Total(); got != len(s.vals) {
				t.Errorf("Total()=%d want %d", got, len(s.vals))
			}
		})
	}
}

// 不变量 3：范围完整——范围外计数为 0，端点桶确实被命中。
func TestRangeCompleteness(t *testing.T) {
	for _, s := range seqs {
		t.Run(s.name, func(t *testing.T) {
			h := fill(t, s.width, s.anchor, s.vals)
			lo, hi, ok := h.Range()
			if !ok {
				t.Fatal("range empty after inserts")
			}
			outsideZero := h.BucketCount(lo-1) == 0 && h.BucketCount(hi+1) == 0
			endpointsHit := h.BucketCount(lo) > 0 && h.BucketCount(hi) > 0
			if !outsideZero || !endpointsHit {
				t.Errorf("range [%d,%d] incomplete", lo, hi)
			}
		})
	}
	t.Run("empty histogram", func(t *testing.T) {
		h := fill(t, 10, 0, nil)
		_, _, ok := h.Range()
		if ok || h.Total() != 0 || h.BucketCount(0) != 0 {
			t.Error("empty histogram must report empty range and zero counts")
		}
	})
}

// 不变量 4：width<=0 可判定失败，且无半成品状态。
func TestNewInvalidWidth(t *testing.T) {
	for _, w := range []int{0, -1, -100} {
		h, err := api.New(w, 0)
		if !errors.Is(err, api.ErrNonPositiveWidth) || h != nil {
			t.Errorf("New(%d)=(%v,%v) want nil,ErrNonPositiveWidth", w, h, err)
		}
	}
}

// 并发：N 个 goroutine 只读同一已喂满实例，结果逐字段相同；不用 sleep。
func TestConcurrentReads(t *testing.T) {
	s := seqs[0]
	h := fill(t, s.width, s.anchor, s.vals)
	lo, hi, _ := h.Range()
	want := batchOf(s.width, s.anchor, s.vals)
	const n = 16
	start := make(chan struct{})
	okCh := make(chan bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lo2, hi2, rok := h.Range()
			ok := rok && lo2 == lo && hi2 == hi && h.Total() == len(s.vals) && h.SelfCheck() == nil
			for k := lo; k <= hi; k++ {
				ok = ok && h.BucketCount(k) == want[k]
			}
			okCh <- ok
		}()
	}
	close(start)
	wg.Wait()
	close(okCh)
	for ok := range okCh {
		if !ok {
			t.Error("concurrent reader observed inconsistent state")
		}
	}
}

// SelfCheck 对内置序列核验四条不变量，必须通过。
func TestSelfCheck(t *testing.T) {
	if err := fill(t, 10, 0, seqs[0].vals).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if err := fill(t, 5, 1, nil).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck on empty: %v", err)
	}
}
