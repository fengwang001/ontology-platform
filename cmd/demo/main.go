package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/audit"
	"ontology/freelist"
	"ontology/handle"
	"ontology/slot"
	"ontology/table"
)

func check(name string, ok bool) {
	if !ok {
		fmt.Println("FAIL", name)
		os.Exit(1)
	}
	fmt.Println("OK", name)
}
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
func getErr(t *table.Table, h handle.Handle) error {
	_, err := t.Get(h)
	return err
}
func snapStr(t *table.Table) string { return fmt.Sprint(t.Slots(), t.FreeIndices(), t.Len()) }

// lastVisits 读取 freelist 的非导出计数器（只读反射，计数器不进公开接口）。
func lastVisits(t *table.Table) int64 {
	fv := reflect.ValueOf(t).Elem().FieldByName("free")
	return fv.Elem().FieldByName("lastVisits").Int()
}
func visitsForCap(capN int) int64 {
	vt := must(table.New(capN))
	vhs := make([]handle.Handle, capN)
	for j := range vhs {
		vhs[j] = must(vt.Insert(j))
	}
	for j := 0; j < capN; j += 2 {
		vt.Remove(vhs[j])
	}
	must(vt.Insert(-1))
	return lastVisits(vt)
}
func abaChurn() (*table.Table, int64) {
	aba := must(table.New(1))
	victim := must(aba.Insert(0))
	aba.Remove(victim)
	var success atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := aba.Get(victim); err == nil {
					success.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 20000; i++ {
		aba.Remove(must(aba.Insert(i)))
	}
	stop.Store(true)
	wg.Wait()
	return aba, success.Load()
}
func main() {
	h := handle.New(7, 42, 99)
	fl := freelist.New(make([]slot.Slot, 3))
	_, a, _ := fl.Take()
	_, b, _ := fl.Take()
	fl.Give(a)
	_, c, _ := fl.Take()
	check("slot-handle-freelist", h.Tag() == 7 && h.Index() == 42 && h.Gen() == 99 &&
		handle.Zero.IsZero() && !h.IsZero() &&
		h.Equal(handle.New(7, 42, 99)) && !h.Equal(handle.New(7, 42, 100)) &&
		a == 0 && b == 1 && c == 2 && fl.Len() == 1)
	tab := must(table.New(4))
	h1 := must(tab.Insert("alpha"))
	v, err := tab.Get(h1)
	check("insert-get", err == nil && v == "alpha" && tab.Len() == 1)
	tab.Remove(h1)
	check("remove-stale", errors.Is(getErr(tab, h1), table.ErrStaleHandle))
	small := must(table.New(1))
	old := must(small.Insert(0))
	small.Remove(old)
	ok := true
	for i := 0; i < 100; i++ {
		small.Remove(must(small.Insert(i)))
		ok = ok && errors.Is(getErr(small, old), table.ErrStaleHandle)
	}
	check("reuse-stale", ok)
	ex := must(table.New(3))
	ha := must(ex.Insert("A"))
	hb := must(ex.Insert("B"))
	var last handle.Handle
	for i := 0; i < handle.MaxGen; i++ {
		last = must(ex.Insert(i))
		ex.Remove(last)
	}
	_, errFull := ex.Insert("x")
	ex.Remove(hb)
	_, errAgain := ex.Insert("y")
	check("exhausted-slot", ex.Slots()[2].Exhausted && getErr(ex, ha) == nil &&
		errors.Is(getErr(ex, last), table.ErrStaleHandle) &&
		errors.Is(errFull, table.ErrFull) && errAgain == nil && ex.Len() == 2)
	ns := must(table.New(3))
	na := must(ns.Insert(1))
	nb := must(ns.Insert(2))
	ns.Remove(nb)
	of := must(must(table.New(1)).Insert(9))
	must(ns.Insert(3))
	must(ns.Insert(4))
	before := snapStr(ns)
	ns.Insert(5)
	for _, hh := range []handle.Handle{handle.Zero, of, nb} {
		getErr(ns, hh)
		ns.Remove(hh)
	}
	check("reject-no-side-effect", snapStr(ns) == before && getErr(ns, na) == nil)
	check("zero-handle", errors.Is(getErr(tab, handle.Zero), table.ErrZeroHandle) &&
		errors.Is(tab.Remove(handle.Zero), table.ErrZeroHandle))
	check("foreign-handle", errors.Is(getErr(ns, of), table.ErrForeignHandle))
	st := must(table.New(1))
	sh := must(st.Insert(1))
	st.Remove(sh)
	e1 := getErr(st, sh)
	must(st.Insert(2))
	check("stale-twice", errors.Is(e1, table.ErrStaleHandle) && getErr(st, sh) == e1)
	check("invariant-equation", audit.Check(ns) == nil && audit.Check(ex) == nil &&
		audit.Check(tab) == nil && audit.Check(small) == nil && audit.Check(st) == nil)
	aba, bad := abaChurn()
	check("aba-concurrent", bad == 0 && audit.Check(aba) == nil)
	counts := [2]int64{visitsForCap(1000), visitsForCap(100000)}
	check(fmt.Sprintf("visits-scaling cap1000=%d cap100000=%d", counts[0], counts[1]),
		counts[0] >= 1 && counts[0] <= 4 && counts[0] == counts[1])
}
