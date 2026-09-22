package ontology

import (
	"math"
	"sync"
	"testing"
)

// TestConcurrentStepsIsolation 多协程并发计算不同向量对，
// 各自的推进步数与结果必须互不串台。需配合 -race 运行。
func TestConcurrentStepsIsolation(t *testing.T) {
	const workers = 32
	const iterations = 200

	// 每个协程使用不同长度的向量对，期望步数各不相同。
	type pair struct {
		a, b  Vector
		steps int
		dot   float64
	}
	pairs := make([]pair, workers)
	for w := 0; w < workers; w++ {
		n := w%7 + 1 // 1..7 个元素，步数随长度变化
		var a, b Vector
		for i := 0; i < n; i++ {
			a = append(a, Element{uint32(i * 2), float64(w + i + 1)})
			b = append(b, Element{uint32(i * 2), float64(w - i)})
		}
		_, st, err := Dot(a, b)
		if err != nil {
			t.Fatalf("协程 %d 的基准 Dot 出错: %v", w, err)
		}
		d, _, err := Dot(a, b)
		if err != nil {
			t.Fatalf("协程 %d 的基准 Dot 出错: %v", w, err)
		}
		pairs[w] = pair{a: a, b: b, steps: st.Steps, dot: d}
	}

	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			p := pairs[w]
			for it := 0; it < iterations; it++ {
				got, st, err := Dot(p.a, p.b)
				if err != nil {
					errs <- err.Error()
					return
				}
				if st.Steps != p.steps {
					errs <- "步数串台"
					return
				}
				if math.Float64bits(got) != math.Float64bits(p.dot) {
					errs <- "结果串台"
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

// TestConcurrentSharedInputs 多协程并发只读同一对向量，验证无数据竞争。
func TestConcurrentSharedInputs(t *testing.T) {
	a := Vector{{0, 1.5}, {3, -2}, {9, 0}, {100, 4}, {1 << 20, -1}}
	b := Vector{{3, 7}, {9, 1}, {100, -1}, {1 << 20, 2}}
	wantDot, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}
	wantCos, _, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 64; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < 200; it++ {
				d, _, err := Dot(a, b)
				if err != nil || math.Float64bits(d) != math.Float64bits(wantDot) {
					t.Error("并发 Dot 结果不一致")
					return
				}
				c, _, err := Cosine(a, b)
				if err != nil || math.Float64bits(c) != math.Float64bits(wantCos) {
					t.Error("并发 Cosine 结果不一致")
					return
				}
			}
		}()
	}
	wg.Wait()
}
