package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/psum"
)

var failed bool

func report(name string, err error) {
	if err != nil {
		failed = true
		fmt.Printf("FAIL %s: %v\n", name, err)
	} else {
		fmt.Printf("OK %s\n", name)
	}
}
func checkEightSteps() (stepErr, prefixErr error) {
	v, _ := psum.New(16, 16)
	steps := []struct {
		del      bool
		k, val   int64
		affected int
		view     map[int64]int64
	}{
		{false, 3, 5, 1, map[int64]int64{3: 5}},
		{false, 7, -2, 1, map[int64]int64{3: 5, 7: 3}},
		{false, 5, 4, 2, map[int64]int64{3: 5, 5: 9, 7: 7}},
		{false, 3, 8, 3, map[int64]int64{3: 8, 5: 12, 7: 10}},
		{false, 5, 4, 0, map[int64]int64{3: 8, 5: 12, 7: 10}},
		{true, 3, 0, 2, map[int64]int64{5: 4, 7: 2}},
		{false, 0, 0, 1, map[int64]int64{0: 0, 5: 4, 7: 2}},
		{false, 7, -6, 1, map[int64]int64{0: 0, 5: 4, 7: -2}},
	}
	for i, s := range steps {
		got, err := 0, error(nil)
		if s.del {
			got, err = v.Del(s.k)
		} else {
			got, err = v.Put(s.k, s.val)
		}
		if err != nil || got != s.affected || !reflect.DeepEqual(v.View(), s.view) {
			stepErr = fmt.Errorf("step %d: affected=%d view=%v err=%v", i+1, got, v.View(), err)
			return
		}
	}
	for k, want := range map[int64]int64{5: 4, 0: 0} {
		if got, err := v.Prefix(k); err != nil || got != want {
			prefixErr = fmt.Errorf("Prefix(%d)=%d,%v, want %d", k, got, err, want)
		}
	}
	for _, k := range []int64{6, 3} {
		if _, err := v.Prefix(k); !errors.Is(err, psum.ErrNotFound) {
			prefixErr = fmt.Errorf("Prefix(%d) err=%v, want ErrNotFound", k, err)
		}
	}
	return
}

func checkRevert() error {
	v, _ := api.New(16, 16)
	_, _ = v.Put(3, 5)
	_, _ = v.Put(7, -2)
	base := v.View()
	_, _ = v.Put(3, 8)
	_, _ = v.Put(3, 5)
	_, _ = v.Put(9, 1)
	_, _ = v.Del(9)
	if !reflect.DeepEqual(v.View(), base) {
		return errors.New("view changed after revert")
	}
	return nil
}

// checkErrors triggers each sentinel; rejections must leave no trace.
func checkErrors() (kinds, noTrace error) {
	v, _ := api.New(4, 1)
	_, _ = v.Put(1, 5) // full: maxKeys=1
	snap := v.View()
	cases := []struct {
		op   func() error
		want error
	}{ // each must fail with its own sentinel
		{func() error { _, e := v.Put(0, 1e10); return e }, api.ErrBadParam},
		{func() error { _, e := v.Put(4, 1); return e }, api.ErrOutOfRange},
		{func() error { _, e := v.Del(2); return e }, api.ErrNotFound},
		{func() error { _, e := v.Prefix(2); return e }, api.ErrNotFound},
		{func() error { _, e := v.Put(2, 1); return e }, api.ErrTooMany},
	}
	for i, c := range cases {
		if e := c.op(); !errors.Is(e, c.want) && kinds == nil {
			kinds = fmt.Errorf("case %d: got %v, want %v", i, e, c.want)
		}
		if !reflect.DeepEqual(v.View(), snap) && noTrace == nil {
			noTrace = fmt.Errorf("case %d left a trace", i)
		}
	}
	if _, e := v.Put(1, 7); e != nil && noTrace == nil {
		noTrace = fmt.Errorf("reuse after rejects: %v", e)
	}
	return kinds, noTrace
}
func checkConcurrent() error {
	v, _ := api.New(1024, 512)
	for i := 0; i < 512; i++ {
		_, _ = v.Put(int64(2*i), int64(i%7-3))
	}
	golden := v.View()
	start := make(chan struct{})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 300 && !bad.Load(); i++ {
				bad.Store(!reflect.DeepEqual(v.View(), golden))
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		return errors.New("readers saw divergent views")
	}
	return nil
}
func main() {
	stepErr, prefixErr := checkEightSteps()
	report("eight-step prefixes & affected", stepErr)
	report("post-step8 Prefix(5/0/6/3)", prefixErr)
	report("random ops match naive", api.SelfCheck())
	report("modify-revert restores view", checkRevert())
	kinds, noTrace := checkErrors()
	report("four decidable errors", kinds)
	report("rejected op leaves state unchanged", noTrace)
	report("visits logarithmic in m", psum.CheckLogarithmic())
	report("concurrent readers identical", checkConcurrent())
	if failed {
		os.Exit(1)
	}
}
