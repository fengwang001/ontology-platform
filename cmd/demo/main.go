// Command demo exercises the delayed task queue end to end.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/dlq"
	"ontology/sched"
)

type op struct {
	k  byte // 's' schedule, 'c' cancel, 't' tick
	id string
	t  int64
}

var eightOps = []op{
	{'s', "a", 5}, {'s', "b", 3}, {'s', "c", 5}, {'s', "d", 2},
	{'c', "b", 0}, {'s', "b", 4}, {'t', "", 5}, {'t', "", 5},
}

// wantActive[k] is the active queue order after the first k ops (k<=6).
var wantActive = [][]string{
	nil, {"a"}, {"b", "a"}, {"b", "a", "c"}, {"d", "b", "a", "c"},
	{"d", "a", "c"}, {"d", "b", "a", "c"},
}

func apply(s *sched.S, ops []op) error {
	for _, o := range ops {
		var err error
		if o.k == 's' {
			err = s.Schedule(o.id, o.t)
		} else if o.k == 'c' {
			err = s.Cancel(o.id)
		} else {
			_, err = s.Tick(o.t)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func eightSteps() bool {
	for k := 1; k <= 6; k++ { // per-step queued state, drained far future
		s := sched.New()
		if apply(s, eightOps[:k]) != nil {
			return false
		}
		if got, e := s.Tick(1 << 62); e != nil || !slices.Equal(got, wantActive[k]) {
			return false
		}
	}
	s := sched.New() // steps 7-8: firing list, then no refire
	if apply(s, eightOps[:6]) != nil {
		return false
	}
	first, e1 := s.Tick(5)
	second, e2 := s.Tick(5)
	return e1 == nil && e2 == nil && slices.Equal(first, []string{"d", "b", "a", "c"}) && len(second) == 0
}

func negativeFireAt() bool {
	s := sched.New()
	if s.Schedule("n", -1) != nil {
		return false
	}
	got, err := s.Tick(0)
	return err == nil && slices.Equal(got, []string{"n"})
}
func errorsNoTrace() bool {
	q := api.New()
	if q.Schedule("a", 10) != nil {
		return false
	}
	if _, e := q.Tick(5); e != nil {
		return false
	}
	errs := []error{q.Schedule("", 1), q.Schedule("a", 11), q.Cancel("z")}
	if _, e := q.Tick(4); e != nil {
		errs = append(errs, e)
	}
	kinds := map[string]bool{}
	for _, e := range errs {
		if e == nil {
			return false
		}
		kinds[e.Error()] = true
	}
	got, err := q.Tick(10) // no trace: a still fires
	return err == nil && len(kinds) == 4 && slices.Equal(got, []string{"a"})
}

func concurrentCancel() bool {
	const N = 100
	q := api.New()
	for i := range N {
		if q.Schedule(fmt.Sprintf("t%d", i), 1) != nil {
			return false
		}
	}
	rng := rand.New(rand.NewSource(1))
	killed := map[string]bool{}
	var wg sync.WaitGroup
	for _, i := range rng.Perm(N)[:N/2] {
		id := fmt.Sprintf("t%d", i)
		killed[id] = true
		wg.Add(1)
		go func() { defer wg.Done(); _ = q.Cancel(id) }()
	}
	wg.Wait()
	got, err := q.Tick(1)
	if err != nil || len(got) != N-N/2 {
		return false
	}
	for _, id := range got {
		if killed[id] {
			return false
		}
	}
	return true
}

func report(name string, ok bool) bool {
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
	return ok
}

func main() {
	ok := true
	ok = report("8-step states; Tick(5)=[d b a c]; repeat empty", eightSteps()) && ok
	ok = report("negative fireAt fires at Tick(0)", negativeFireAt()) && ok
	ok = report("Tick agrees with naive reference (SelfCheck)", api.New().SelfCheck()) && ok
	ok = report("four distinct judgeable errors, no trace", errorsNoTrace()) && ok
	ok = report("pop checks sublinear in heap size m", dlq.PopCheckSublinear()) && ok
	ok = report("concurrent cancel fires exactly survivors", concurrentCancel()) && ok
	if !ok {
		os.Exit(1)
	}
}
