// Command demo demonstrates lock-free copy-on-write snapshot reads.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/snapshot"
	"ontology/ver"
)

func okLine(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

func main() {
	v := ver.New(1, map[string]string{"A": "1"})
	c := ver.Clone(v)
	c["A"] = "9" // mutating the clone must not touch the published version
	got, _ := v.Get("A")
	fmt.Printf("ver clone isolation: %s\n", okLine(got == "1" && v.Len() == 1))

	s := snapshot.NewStore()
	s.Update("A", "1")
	s.Update("B", "2")
	h := s.Snapshot() // pins id2 {A:1,B:2}
	r4, _ := s.Read("A")
	s.Update("A", "9")
	r6, _ := s.Read("A")
	r7, _ := h.Read("A")
	k8 := s.ReadKeys([]string{"A", "B"})
	s.Update("B", "5")
	k10 := s.ReadKeys([]string{"A", "B"})
	fmt.Printf("steps 4,6,7: %s (%s,%s,%s)\n", okLine(r4 == "1" && r6 == "9" && r7 == "1"), r4, r6, r7)
	fmt.Printf("steps 8,10 one-version ReadKeys: %s %v %v\n",
		okLine(reflect.DeepEqual(k8, map[string]string{"A": "9", "B": "2"}) &&
			reflect.DeepEqual(k10, map[string]string{"A": "9", "B": "5"})), k8, k10)
	fmt.Printf("read amplification bounded + snapshot self-check: %s\n", okLine(snapshot.NewStore().SelfCheck() == nil))

	a := api.New(8, 4)
	_ = a.Update("a", "1")
	before := a.Snapshot()
	errs := []error{a.Update("", "x"), a.Update("x", ""), a.Update("toolongkey", "x")}
	_ = a.Update("b", "2")
	_ = a.Update("c", "3")
	_ = a.Update("d", "4")
	errs = append(errs, a.Update("e", "5"))
	after := a.Snapshot()
	distinct := map[error]bool{}
	for _, e := range errs {
		distinct[e] = true
	}
	four := errors.Is(errs[0], api.ErrEmptyKey) && errors.Is(errs[1], api.ErrEmptyValue) &&
		errors.Is(errs[2], api.ErrKeyTooLong) && errors.Is(errs[3], api.ErrTooManyKeys) && len(distinct) == 4
	unchanged := after.ID() == before.ID()+3 && after.Len() == 4
	usable, _ := a.Read("d")
	fmt.Printf("four distinct sentinel errors: %s\n", okLine(four))
	fmt.Printf("rejected leaves state unchanged, still usable: %s\n", okLine(unchanged && usable == "4"))
	fmt.Printf("api self-check: %s\n", okLine(api.New(16, 64).SelfCheck() == nil))

	p := api.New(64, 1<<20)
	_ = p.Update("A", "0")
	_ = p.Update("B", "0")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // writer: real (A,B) states satisfy B==A or B==A-1
		defer wg.Done()
		for r := 1; r <= 2000; r++ {
			_ = p.Update("A", strconv.Itoa(r))
			_ = p.Update("B", strconv.Itoa(r))
		}
	}()
	bad := atomic.Bool{}
	var rwg sync.WaitGroup
	for g := 0; g < 8; g++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for i := 0; i < 5000; i++ {
				m := p.ReadKeys([]string{"A", "B"})
				x, _ := strconv.Atoi(m["A"])
				y, _ := strconv.Atoi(m["B"])
				if !(y == x || y == x-1) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	rwg.Wait()
	fmt.Printf("concurrent readers always see a whole version: %s\n", okLine(!bad.Load()))
}
