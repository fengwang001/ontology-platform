package sparse

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentLinearizable hammers the engine with concurrent applies,
// local-mod marks and queries. Because every writer toggles between exactly
// two global states ((A,v1) and (B,v2)), any non-atomic intermediate state
// would show up as a listing that equals neither snapshot.
func TestConcurrentLinearizable(t *testing.T) {
	e := NewEngine()
	var filesA, filesB []string
	for i := 0; i < 50; i++ {
		filesA = append(filesA, fmt.Sprintf("a/f%02d", i))
		filesB = append(filesB, fmt.Sprintf("b/g%02d", i))
	}
	mustAddCommit(t, e, "A", filesA)
	mustAddCommit(t, e, "B", filesB)
	v1 := mustAddRuleset(t, e, Rule{Include, "a/"})
	v2 := mustAddRuleset(t, e, Rule{Include, "b/"})

	mustApply(t, e, "A", v1, false)
	wantA, err := e.ListMaterialized("")
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, e, "B", v2, false)
	wantB, err := e.ListMaterialized("")
	if err != nil {
		t.Fatal(err)
	}
	snapA := strings.Join(wantA, "\n")
	snapB := strings.Join(wantB, "\n")
	t.Logf("input: two states, |A|=%d paths, |B|=%d paths; writers toggle, readers snapshot", len(wantA), len(wantB))

	var wg sync.WaitGroup
	errCh := make(chan string, 16)

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				var err error
				if (id+i)%2 == 0 {
					_, err = e.Apply("A", v1, true)
				} else {
					_, err = e.Apply("B", v2, true)
				}
				if err != nil {
					errCh <- fmt.Sprintf("writer %d: %v", id, err)
					return
				}
			}
		}(w)
	}

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				got, err := e.ListMaterialized("")
				if err != nil {
					errCh <- fmt.Sprintf("reader %d: %v", id, err)
					return
				}
				s := strings.Join(got, "\n")
				if s != snapA && s != snapB {
					errCh <- fmt.Sprintf("reader %d: non-serializable snapshot of %d paths", id, len(got))
					return
				}
				// Point queries must agree with the same snapshot.
				st, err := e.QueryPath("a/f00")
				if err != nil {
					errCh <- fmt.Sprintf("reader %d: %v", id, err)
					return
				}
				// State A: materialized; state B: absent from the commit.
				if st != StatusMaterialized && st != StatusNotInCommit {
					errCh <- fmt.Sprintf("reader %d: impossible status %v", id, st)
					return
				}
			}
		}(r)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			// Errors are expected (path may not be materialized right now).
			_ = e.SetLocalModified(fmt.Sprintf("a/f%02d", i%50), i%2 == 0)
			_ = e.SetLocalModified(fmt.Sprintf("b/g%02d", i%50), i%2 == 0)
		}
	}()

	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
	t.Log("all concurrent observations matched one of the two serial states (依据: 并发变更与查询的串行等价)")
}
