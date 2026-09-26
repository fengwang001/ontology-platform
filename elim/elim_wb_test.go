package elim

import (
	"math"
	"sync"
	"testing"
)

func TestFailureAtomic(t *testing.T) {
	e := New()
	good := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	bads := []struct {
		a    []float64
		n    int
		want error
	}{
		{good, 0, ErrEmpty},
		{good, -3, ErrEmpty},
		{[]float64{1, 2, 3}, 2, ErrDimension},
		{good, 4, ErrDimension},
		{[]float64{math.NaN(), 0, 0, 1}, 2, ErrNonFinite},
		{[]float64{1, 0, 0, math.Inf(-1)}, 2, ErrNonFinite},
	}
	seen := map[error]bool{}
	for _, b := range bads {
		ops := e.ops.Load()
		if _, err := e.Det(b.a, b.n); err != b.want {
			t.Errorf("n=%d got %v want %v", b.n, err, b.want)
		}
		if e.ops.Load() != ops {
			t.Errorf("counter moved on rejected call")
		}
		seen[b.want] = true
	}
	if len(seen) != 3 {
		t.Errorf("sentinel errors not distinct: %v", seen)
	}
	if d, err := e.Det(good, 3); err != nil || d != 2 { // 拒后仍可用
		t.Errorf("unusable after rejection: %v %v", d, err)
	}
}

func TestUpperTriangularZeroOps(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} { // 多档规模
		e := New()
		m := make([]float64, n*n)
		for i := 0; i < n; i++ {
			m[i*n+i] = 2 // 上三角：i>j 全零
		}
		if d, err := e.Det(m, n); err != nil || d != math.Pow(2, float64(n)) {
			t.Fatalf("n=%d got %v,%v", n, d, err)
		}
		if ops := e.ops.Load(); ops != 0 {
			t.Errorf("n=%d mul-sub ops = %d, want 0", n, ops)
		}
	}
}

func TestConcurrentDet(t *testing.T) {
	e := New()
	m := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	const G = 32
	var wg sync.WaitGroup
	bits := make([]uint64, G)
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				v, err := e.Det(m, 3)
				if err != nil {
					t.Error(err)
					return
				}
				bits[g] = math.Float64bits(v)
			}
		}(g)
	}
	wg.Wait()
	for g := 1; g < G; g++ {
		if bits[g] != bits[0] {
			t.Fatalf("goroutine results differ: %x vs %x", bits[g], bits[0])
		}
	}
}
