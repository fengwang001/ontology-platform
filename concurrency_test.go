package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// sharedTree exercises And/Or/Not/IsNull plus short-circuiting:
// And( Or(x>0, IsNull(y)), Not(Eq(name,"root")) ) — 2 comparison leaves
// when x>0 is True, 3 when it is False.
func sharedTree() Pred {
	return AndP(
		OrP(GtTo("x", int64(0)), IsNull("y")),
		NotP(EqTo("name", "root")),
	)
}

func TestConcurrentEvaluationCountsDoNotLeak(t *testing.T) {
	e := NewEvaluator(16)
	p := sharedTree()
	const workers = 32
	const iters = 200

	var wg sync.WaitGroup
	errs := make(chan error, workers*iters)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				// Each goroutine uses its own attribute map with a
				// deterministic expected (value, leafCount) pair.
				pos := (w+i)%2 == 0
				attrs := map[string]any{"name": fmt.Sprintf("u%d", w)}
				wantV := True
				wantN := 2
				if pos {
					attrs["x"] = int64(1) // Or short-circuits on True
				} else {
					// Or falls through to IsNull (False: y present),
					// so And short-circuits after 1 leaf.
					attrs["x"] = int64(-1)
					attrs["y"] = "set"
					wantV = False
					wantN = 1
				}
				v, n, err := e.EvalCount(p, attrs)
				if err != nil {
					errs <- err
					return
				}
				if v != wantV || n != wantN {
					errs <- fmt.Errorf("worker %d: got (%s, %d), want (%s, %d)", w, v, n, wantV, wantN)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestEvaluationIsPureAndRepeatable(t *testing.T) {
	e := NewEvaluator(16)
	p := sharedTree()
	attrs := map[string]any{"x": int64(1), "name": "alice"}
	before := fmt.Sprintf("%#v", p)

	v1, n1, err1 := e.EvalCount(p, attrs)
	v2, n2, err2 := e.EvalCount(p, attrs)
	if v1 != v2 || n1 != n2 || (err1 == nil) != (err2 == nil) {
		t.Errorf("repeated eval differs: (%s,%d,%v) vs (%s,%d,%v)", v1, n1, err1, v2, n2, err2)
	}
	// Neither the tree nor the attribute map may be mutated.
	if len(attrs) != 2 || attrs["x"] != int64(1) || attrs["name"] != "alice" {
		t.Errorf("attrs mutated: %v", attrs)
	}
	if fmt.Sprintf("%#v", p) != before {
		t.Errorf("predicate tree mutated: %#v", p)
	}
}
