package bloom

import (
	"fmt"
	"sync"
	"testing"
)

// TestLastBitsConstant 证明 Test 是 O(k) 定位：无论已插入多少元素，
// 最近一次 Test 检查的位数恒等于 k，不随元素个数增长。
func TestLastBitsConstant(t *testing.T) {
	const k = 5
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			f, err := New(1<<20, k, n) // 位数组足够大，避免写满
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if err := f.Add([]byte(fmt.Sprintf("e-%d", i))); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.Test([]byte("e-0")); err != nil {
				t.Fatal(err)
			}
			if got := f.lastBits.Load(); got != int64(k) {
				t.Fatalf("checked bits = %d, want %d", got, k)
			}
		})
	}
}

// TestSpecSequence 钉住 NOTES.md 第三节的八行分步表（m=10, k=3）。
func TestSpecSequence(t *testing.T) {
	f, err := New(10, 3, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"a", "b", "c"} {
		if err := f.Add([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		x    string
		want bool
	}{{"o", true}, {"a", true}, {"d", false}, {"b", true}, {"x", false}} {
		if got, err := f.Test([]byte(c.x)); err != nil || got != c.want {
			t.Fatalf("Test(%q) = %v, %v; want %v", c.x, got, err, c.want)
		}
	}
	wantBits := map[int]bool{0: true, 1: true, 3: true, 5: true, 6: true, 7: true, 8: true, 9: true}
	for i, b := range f.Snapshot() {
		if b != wantBits[i] {
			t.Fatalf("bit %d = %v, want %v", i, b, wantBits[i])
		}
	}
}

// TestConcurrent 并发 Add/Test/Count：N 个写者加不同元素，N 个读者读同一元素。
func TestConcurrent(t *testing.T) {
	const n = 128
	f, err := New(1<<16, 3, n+1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add([]byte("shared")); err != nil {
		t.Fatal(err)
	}
	results := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if err := f.Add([]byte(fmt.Sprintf("c-%d", i))); err != nil {
				t.Error(err)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			ok, _ := f.Test([]byte("shared"))
			_ = f.Count()
			results[i] = ok
		}(i)
	}
	wg.Wait()
	if f.Count() != n+1 {
		t.Fatalf("count = %d, want %d", f.Count(), n+1)
	}
	for i := 0; i < n; i++ {
		if !results[i] {
			t.Fatal("concurrent readers disagreed")
		}
		if ok, _ := f.Test([]byte(fmt.Sprintf("c-%d", i))); !ok {
			t.Fatalf("false negative on c-%d", i)
		}
	}
}
