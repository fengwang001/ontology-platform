// Command demo exercises the partition affinity router end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"ontology/api"
	"ontology/part"
	"ontology/router"
)

// readScanned reads the unexported lastMigrateScanned directly; it has no
// exported accessor, so the demo locates the field itself.
func readScanned(r *router.Router) int {
	f := reflect.ValueOf(r).Elem().FieldByName("lastMigrateScanned")
	return *(*int)(unsafe.Pointer(f.UnsafeAddr()))
}

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	failed = true
	fmt.Println("FAIL", name)
}

func main() {
	p := part.New()
	p.Add("a")
	p.Add("b")
	check("part: Active add/count, drain, takeall",
		p.State() == part.Active && p.Len() == 2 && p.BeginDrain() == nil && len(p.TakeAll()) == 2 && p.Len() == 0)
	s := api.New(0, 1, 2)
	steps := []func() error{
		func() error { return s.Assign("a", 0) }, func() error { return s.Assign("b", 0) },
		func() error { return s.Assign("c", 1) }, func() error { return s.Assign("d", 2) },
		func() error { return s.BeginDrain(2) }, func() error { return s.Assign("e", 2) },
		func() error { return s.Put("d", "x") }, func() error { return s.Migrate(2, 0) },
	}
	wantErr := []bool{false, false, false, false, false, true, true, false}
	wantCnt := [][3]int{{1, 0, 0}, {2, 0, 0}, {2, 1, 0}, {2, 1, 1}, {2, 1, 1}, {2, 1, 1}, {2, 1, 1}, {3, 1, 0}}
	detail, eightOK := "", true
	for i, st := range steps {
		err := st()
		got := [3]int{s.Count(0), s.Count(1), s.Count(2)}
		v := "✓"
		if wantErr[i] {
			v = "✗"
		}
		detail += fmt.Sprintf(" %d%s%v", i+1, v, got)
		if (err != nil) != wantErr[i] || got != wantCnt[i] {
			eightOK = false
		}
	}
	check("eight steps (verdict+C0,C1,C2):"+detail, eightOK)
	v, ok := s.Get("d")
	check("post-migrate: Get(d) empty, d owned by Active P0",
		!ok && v == "" && s.Put("d", "z") == nil && s.Count(0) == 3 && s.Count(2) == 0)
	s2 := api.New(0, 1)
	_ = s2.Assign("a", 0)
	_ = s2.Assign("b", 0)
	_ = s2.BeginDrain(0)
	_ = s2.Migrate(0, 1)
	check("batch recount: SelfCheck nil, sum==accepted Assigns",
		s2.SelfCheck() == nil && s2.Count(0)+s2.Count(1) == 2)
	e1, e2, e3, e4 := s.Assign("z", 99), s.Put("nobody", "v"), s.Assign("qq", 2), s.Migrate(1, 1)
	check("four distinct sentinel errors",
		errors.Is(e1, router.ErrPartitionNotFound) && errors.Is(e2, router.ErrNoAffinity) &&
			errors.Is(e3, router.ErrAffinityConflict) && errors.Is(e4, router.ErrInvalidMigrate) &&
			e1 != e2 && e2 != e3 && e3 != e4)
	before := [3]int{s.Count(0), s.Count(1), s.Count(2)}
	trace := s.Assign("", 0) == nil || s.Assign("n", 99) == nil || s.Put("n", "v") == nil ||
		s.Migrate(1, 0) == nil || s.SelfCheck() != nil
	check("rejected ops leave no trace", !trace && before == [3]int{s.Count(0), s.Count(1), s.Count(2)})
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		r := router.New()
		for i := 0; i < m; i++ {
			_ = r.AddPartition(i)
			_ = r.Assign(fmt.Sprintf("k%d", i), i)
		}
		_ = r.BeginDrain(m - 1)
		_ = r.Migrate(m-1, 0)
		bigOK = bigOK && readScanned(r) == 1
	}
	check("large m migrate scans O(1) partitions", bigOK)
	r := router.New()
	_ = r.AddPartition(0)
	_ = r.AddPartition(1)
	for i := 0; i < 64; i++ {
		_ = r.Assign(fmt.Sprintf("k%d", i), 1)
		_ = r.Put(fmt.Sprintf("k%d", i), "v")
	}
	_ = r.BeginDrain(1)
	var wg sync.WaitGroup
	started, stop := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var torn, mismatch atomic.Bool
	reader := func() {
		defer wg.Done()
		for {
			if c := r.Count(1); c != 64 && c != 0 { // single atomic read: only pre/post
				torn.Store(true)
			}
			for i := 0; i < 64; i++ { // per-key Get identical before and after
				if v, ok := r.Get(fmt.Sprintf("k%d", i)); !ok || v != "v" {
					mismatch.Store(true)
				}
			}
			once.Do(func() { close(started) })
			select {
			case <-stop:
				return
			default:
			}
		}
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go reader()
	}
	<-started
	_ = r.Migrate(1, 0) // readers above can only have seen the pre state
	close(stop)
	wg.Wait()
	check("concurrent reads see only pre/post migrate",
		!torn.Load() && !mismatch.Load() && r.Count(1) == 0 && r.Count(0) == 64)
	if failed {
		os.Exit(1)
	}
}
