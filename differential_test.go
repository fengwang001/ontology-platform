package ftl

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
)

func TestRandomDifferentialWithTrace(t *testing.T) {
	cfg := Config{
		BlockCount:    9,
		PagesPerBlock: 3,
		LogicalPages:  12,
		LowWatermark:  2,
		HighWatermark: 6,
		EraseLimit:    4,
		WearThreshold: 1,
	}
	actual, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := NewNaive(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var actualLog bytes.Buffer
	var referenceLog bytes.Buffer
	tracedActual := NewTracer("actual", actual, &actualLog)
	tracedReference := NewTracer("naive", reference, &referenceLog)
	rng := rand.New(rand.NewPCG(1597, 42))

	for op := 0; op < 3000; op++ {
		lpn := rng.IntN(cfg.LogicalPages + 1)
		switch rng.IntN(10) {
		case 0, 1:
			errActual := tracedActual.Discard(lpn)
			errReference := tracedReference.Discard(lpn)
			if !errors.Is(errActual, errReference) {
				t.Fatalf("op %d discard(%d): actual=%v naive=%v\nactual log:\n%s\nnaive log:\n%s",
					op, lpn, errActual, errReference, actualLog.String(), referenceLog.String())
			}
		case 2, 3:
			physicalActual, errActual := tracedActual.Read(lpn)
			physicalReference, errReference := tracedReference.Read(lpn)
			if !errors.Is(errActual, errReference) || physicalActual != physicalReference {
				t.Fatalf("op %d read(%d): actual=(%+v,%v) naive=(%+v,%v)",
					op, lpn, physicalActual, errActual, physicalReference, errReference)
			}
		default:
			errActual := tracedActual.Write(lpn)
			errReference := tracedReference.Write(lpn)
			if !errors.Is(errActual, errReference) {
				t.Fatalf("op %d write(%d): actual=%v naive=%v\nactual log:\n%s\nnaive log:\n%s",
					op, lpn, errActual, errReference, actualLog.String(), referenceLog.String())
			}
		}
		assertModelEqual(t, actual, reference)
	}

	t.Logf("actual operation trace:\n%s", actualLog.String())
	t.Logf("naive operation trace:\n%s", referenceLog.String())
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	cfg := testConfig()
	cfg.LogicalPages = 32
	f := mustNew(t, cfg)
	var wg sync.WaitGroup

	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				lpn := (worker*7 + i) % cfg.LogicalPages
				_ = f.Write(lpn)
				_, _ = f.Read(lpn)
				if i%5 == 0 {
					_ = f.Discard(lpn)
				}
			}
		}(worker)
	}
	wg.Wait()

	stats := f.Stats()
	valid, invalid, freePages := countPageStates(f.PhysicalPages())
	if valid+invalid+freePages != cfg.BlockCount*cfg.PagesPerBlock {
		t.Fatalf("page count conservation failed: %d+%d+%d", valid, invalid, freePages)
	}
	if valid != f.mappedPages {
		t.Fatalf("valid pages = %d, mapped = %d", valid, f.mappedPages)
	}
	if stats.PhysicalPrograms < stats.LogicalWrites {
		t.Fatalf("physical programs %d < logical writes %d",
			stats.PhysicalPrograms, stats.LogicalWrites)
	}
}
