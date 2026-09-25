package sparse

import (
	"fmt"
	"math"
	"sync"
	"testing"
)

// TestConcurrentStepsDoNotBleed 在 -race 下并发计算多对向量，
// 断言每次调用的 Steps 只属于本次归并，计数器互不串台，
// 且结果与串行时逐位一致。
func TestConcurrentStepsDoNotBleed(t *testing.T) {
	type pair struct {
		a, b Vector
		want DotResult
	}
	pairs := []pair{
		{Vector{{0, 1}, {1e9, 2}}, Vector{{1, 3}, {1e9, 4}}, DotResult{Dot: 8, Steps: 3, ExplicitZeros: 0}},
		{Vector{{0, 0}, {2, 1}}, Vector{{2, 2}, {3, 0}}, DotResult{Dot: 2, Steps: 2, ExplicitZeros: 2}},
		{Vector{{5, -3}}, Vector{{5, 6}}, DotResult{Dot: -18, Steps: 1, ExplicitZeros: 0}},
		{Vector{{1, 1}, {2, 1}}, Vector{{1, 1}, {2, 1}}, DotResult{Dot: 2, Steps: 2, ExplicitZeros: 0}},
	}

	const goroutines = 64
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*len(pairs))
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for k := 0; k < 25; k++ {
				for _, p := range pairs {
					r, err := Dot(p.a, p.b)
					if err != nil {
						errCh <- err
						return
					}
					if math.Float64bits(r.Dot) != math.Float64bits(p.want.Dot) ||
						r.Steps != p.want.Steps ||
						r.ExplicitZeros != p.want.ExplicitZeros {
						errCh <- mismatchError(r, p.want)
						return
					}
					if _, _, err := Cosine(p.a, p.b); err != nil {
						errCh <- err
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

type stepMismatch struct{ got, want DotResult }

func (e stepMismatch) Error() string {
	return "steps/result bleed: got " +
		formatResult(e.got) + " want " + formatResult(e.want)
}

func mismatchError(got, want DotResult) error {
	return stepMismatch{got, want}
}

func formatResult(r DotResult) string {
	return fmt.Sprintf("{Dot:%v Steps:%d Zeros:%d}", r.Dot, r.Steps, r.ExplicitZeros)
}
