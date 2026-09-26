package api_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

func randSeq(r *rand.Rand, n int) [][2]int64 {
	out := make([][2]int64, n)
	for i, v := range r.Perm(n) {
		out[i] = [2]int64{int64(v*2 + 1), int64(r.IntN(9) + 1)}
	}
	return out
}

// allSeqs 表驱动用例：固定序列 + 多档规模 × 多个随机种子的随机插入顺序。
func allSeqs() map[string][][2]int64 {
	seqs := map[string][][2]int64{
		"spec4": {{30, 3}, {10, 3}, {40, 3}, {20, 3}},
		"left":  {{5, 1}, {1, 10}, {9, 1}},
	}
	for _, n := range []int{1, 2, 3, 17, 100, 1000, 10000} {
		for seed := uint64(0); seed < 3; seed++ {
			seqs[fmt.Sprintf("rand-%d-%d", n, seed)] = randSeq(rand.New(rand.NewPCG(seed, uint64(n))), n)
		}
	}
	return seqs
}

func build(t *testing.T, seq [][2]int64) *api.Store {
	t.Helper()
	s := api.New()
	for _, p := range seq {
		if err := s.Insert(p[0], p[1]); err != nil {
			t.Fatalf("insert %v: %v", p, err)
		}
	}
	return s
}

func naiveMedian(seq [][2]int64) int64 {
	s := append([][2]int64(nil), seq...)
	sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] })
	var W, p int64
	for _, e := range s {
		W += e[1]
	}
	for _, e := range s {
		if p += e[1]; 2*p >= W {
			return e[0]
		}
	}
	return 0
}

// TestMedianMatchesNaive 不变量 3：与朴素参照一致。
func TestMedianMatchesNaive(t *testing.T) {
	for name, seq := range allSeqs() {
		if got, _ := build(t, seq).Median(); got != naiveMedian(seq) {
			t.Errorf("%s: got %d, naive %d", name, got, naiveMedian(seq))
		}
	}
}

// TestSideWeightInvariant 不变量 1：两侧权重约束；不变量 2：最小性。
func TestSideWeightInvariant(t *testing.T) {
	for name, seq := range allSeqs() {
		s := build(t, seq)
		m, _ := s.Median()
		var below, above int64
		for _, p := range seq {
			if p[0] < m {
				below += p[1]
			} else if p[0] > m {
				above += p[1]
			}
		}
		W := s.Total()
		if 2*below > W || 2*above > W {
			t.Errorf("%s: below=%d above=%d W=%d", name, below, above, W)
		}
		if 2*below >= W { // 最小性：更小的 value 都到不了 W/2
			t.Errorf("%s: smaller value already reaches W/2 (m=%d)", name, m)
		}
	}
}

// TestRejectedOpsNoStateChange 不变量 4 + 故障注入：三类错误可判定且互异，被拒不留痕。
func TestRejectedOpsNoStateChange(t *testing.T) {
	if _, err := api.New().Median(); !errors.Is(err, api.ErrEmpty) {
		t.Fatalf("empty median: want ErrEmpty, got %v", err)
	}
	rejects := []struct {
		v, w int64
		want error
	}{
		{5, 9, api.ErrDuplicateValue}, {99, 0, api.ErrNonPositiveWeight}, {99, -7, api.ErrNonPositiveWeight},
	}
	for _, tc := range rejects {
		s := build(t, [][2]int64{{5, 2}, {15, 3}, {25, 4}})
		wBefore, mBefore := s.Total(), func() int64 { m, _ := s.Median(); return m }()
		if err := s.Insert(tc.v, tc.w); !errors.Is(err, tc.want) {
			t.Fatalf("insert(%d,%d): want %v, got %v", tc.v, tc.w, tc.want, err)
		}
		if mAfter, _ := s.Median(); s.Total() != wBefore || mAfter != mBefore {
			t.Fatalf("insert(%d,%d) changed state", tc.v, tc.w)
		}
		if err := s.Insert(35, 1); err != nil { // 被拒后仍可正常使用
			t.Fatalf("insert after reject: %v", err)
		}
	}
	distinct := map[error]bool{api.ErrEmpty: true, api.ErrDuplicateValue: true, api.ErrNonPositiveWeight: true}
	if len(distinct) != 3 {
		t.Fatal("sentinel errors must be distinct")
	}
}
func TestConcurrentMedianConsistent(t *testing.T) {
	s := build(t, randSeq(rand.New(rand.NewPCG(1, 2)), 500))
	want, _ := s.Median()
	var wg sync.WaitGroup
	res := make(chan int64, 32*50)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				m, err := s.Median()
				if err != nil || s.Total() <= 0 || s.SelfCheck() != nil {
					t.Errorf("concurrent call: err=%v", err)
					return
				}
				res <- m
			}
		}()
	}
	wg.Wait()
	close(res)
	for m := range res {
		if m != want {
			t.Fatalf("concurrent median %d != %d", m, want)
		}
	}
}
