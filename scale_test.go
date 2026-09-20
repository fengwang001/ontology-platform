package ontology

import (
	"math"
	"math/rand"
	"sync"
	"testing"
)

// 百万级总权重下唯一值个数仍然很小，且结果与展开语义一致。
func TestMillionTotalWeightFewUniques(t *testing.T) {
	q := New()
	const uniques = 50
	for i := 0; i < uniques; i++ {
		if err := q.AddWeighted(float64(i*10), 25000); err != nil {
			t.Fatalf("AddWeighted: %v", err)
		}
	}
	if got := q.TotalWeight(); got != uniques*25000 {
		t.Fatalf("TotalWeight = %d, want %d", got, uniques*25000)
	}
	if q.TotalWeight() < 1_000_000 {
		t.Fatalf("TotalWeight = %d, want >= 1e6", q.TotalWeight())
	}
	if got := q.UniqueCount(); got != uniques {
		t.Fatalf("UniqueCount = %d, want %d (未展开)", got, uniques)
	}
	// 抽查若干分位点：N=1,250,000，值 v 占据展开区间 [v/10*25000, ...)。
	cases := map[float64]float64{0: 0, 1: 490, 0.5: 245, 0.75: 370}
	for p, want := range cases {
		if got := mustQuantile(t, q, p, Linear); got != want {
			t.Fatalf("Linear(%v) = %v, want %v", p, got, want)
		}
	}
}

// 同一批样本以打乱顺序加入，任意 p 下两种口径结果逐位相同。
func TestShuffledInsertOrderBitIdentical(t *testing.T) {
	const n = 500
	samples := make([]float64, n)
	rng := rand.New(rand.NewSource(42))
	for i := range samples {
		samples[i] = math.Round(rng.NormFloat64()*100) / 4 // 制造重复值
	}
	base := New()
	for _, v := range samples {
		mustAdd(t, base, v)
	}
	for trial := 0; trial < 5; trial++ {
		perm := rand.New(rand.NewSource(int64(trial))).Perm(n)
		q := New()
		for _, idx := range perm {
			mustAdd(t, q, samples[idx])
		}
		for p := 0.0; p <= 1.0; p += 0.005 {
			for _, m := range []Method{NearestRank, Linear} {
				a := mustQuantile(t, base, p, m)
				b := mustQuantile(t, q, p, m)
				if math.Float64bits(a) != math.Float64bits(b) {
					t.Fatalf("trial=%d p=%v method=%v: %v != %v",
						trial, p, m, a, b)
				}
			}
		}
	}
}

// 并发查询安全且互不影响（用 -race 验证）。
func TestConcurrentQueries(t *testing.T) {
	q := New()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 1000; i++ {
		mustAdd(t, q, rng.NormFloat64()*10)
	}
	ps := []float64{0, 0.01, 0.25, 0.5, 0.75, 0.99, 1}
	want := map[[2]interface{}]float64{}
	for _, p := range ps {
		for _, m := range []Method{NearestRank, Linear} {
			want[[2]interface{}{p, m}] = mustQuantile(t, q, p, m)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for iter := 0; iter < 500; iter++ {
				p := ps[(g+iter)%len(ps)]
				for _, m := range []Method{NearestRank, Linear} {
					got, err := q.Quantile(p, m)
					if err != nil {
						t.Errorf("Quantile: %v", err)
						return
					}
					if math.Float64bits(got) !=
						math.Float64bits(want[[2]interface{}{p, m}]) {
						t.Errorf("p=%v method=%v: got %v, want %v",
							p, m, got, want[[2]interface{}{p, m}])
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

// 查询不修改内部状态：重复查询结果一致，计数不变。
func TestQueriesDoNotMutateState(t *testing.T) {
	q := New()
	mustAdd(t, q, 3, 1, 4, 1, 5, 9, 2, 6)
	u0, w0 := q.UniqueCount(), q.TotalWeight()
	first := mustQuantile(t, q, 0.3, Linear)
	for i := 0; i < 100; i++ {
		if got := mustQuantile(t, q, 0.3, Linear); got != first {
			t.Fatalf("iter %d: got %v, want %v", i, got, first)
		}
	}
	if q.UniqueCount() != u0 || q.TotalWeight() != w0 {
		t.Fatal("查询修改了内部状态")
	}
}
