package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"

	"ontology/api"
	"ontology/kv"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
	}
	fmt.Println(name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// traceOK replays the seven-step sequence of NOTES.md exactly.
func traceOK() bool {
	st := api.New()
	s := st.Open()
	e := func(v string, n int64) kv.Entry { return kv.Entry{Val: v, Ver: n} }
	vw := func(a, b, c kv.Entry) (o [3]map[string]kv.Entry) {
		for i, x := range [3]kv.Entry{a, b, c} {
			if o[i] = map[string]kv.Entry{}; x.Ver > 0 {
				o[i]["k"] = x
			}
		}
		return
	}
	var r1, r2 kv.Entry
	ops := []func(){
		func() { s.Write("k", "A") },
		func() { v, n, _ := s.Read("k"); r1 = e(v, n) },
		func() { st.Sync(1) },
		func() { s.Write("k", "B") },
		func() { st.Sync(2) },
		func() { s.Write("k", "C") },
		func() { v, n, _ := s.Read("k"); r2 = e(v, n) },
	}
	want := [7][3]kv.Entry{
		{e("A", 1), {}, {}}, {e("A", 1), {}, {}}, {e("A", 1), e("A", 1), {}}, {e("B", 2), e("A", 1), {}},
		{e("B", 2), e("A", 1), e("B", 2)}, {e("C", 3), e("A", 1), e("B", 2)}, {e("C", 3), e("A", 1), e("B", 2)},
	}
	for i, op := range ops {
		op()
		if !reflect.DeepEqual(st.View(), vw(want[i][0], want[i][1], want[i][2])) {
			return false
		}
	}
	return r1 == e("A", 1) && r2 == e("C", 3)
}

// viewRefOK checks View()[0] against the naive reference.
func viewRefOK() bool {
	st := api.New()
	s := st.Open()
	ref := map[string]kv.Entry{}
	for i := 0; i < 50; i++ {
		key, val := fmt.Sprintf("k%d", i%7), fmt.Sprintf("v%d", i)
		v, err := s.Write(key, val)
		if err != nil || (i%4 == 0 && st.Sync(1+i%2) != nil) {
			return false
		}
		ref[key] = kv.Entry{Val: val, Ver: v}
	}
	return reflect.DeepEqual(st.View()[0], ref)
}

// faultsOK: (1) three fault classes map to three distinct sentinels;
// (2) rejected ops leave no trace and the store keeps working.
func faultsOK() (distinct, noTrace bool) {
	st := api.New()
	s := st.Open()
	s.Write("k", "a")
	before := st.View()
	_, we := s.Write("", "x")
	_, _, re := s.Read("")
	se0, se9 := st.Sync(0), st.Sync(9)
	d := st.Open()
	d.Close()
	_, ce := d.Write("k", "z")
	_, _, cre := d.Read("k")
	distinct = errors.Is(we, api.ErrEmptyKey) && errors.Is(re, api.ErrEmptyKey) &&
		errors.Is(se0, api.ErrBadIndex) && errors.Is(se9, api.ErrBadIndex) &&
		errors.Is(ce, api.ErrClosed) && errors.Is(cre, api.ErrClosed) &&
		!errors.Is(api.ErrEmptyKey, api.ErrBadIndex) &&
		!errors.Is(api.ErrBadIndex, api.ErrClosed) && !errors.Is(api.ErrClosed, api.ErrEmptyKey)
	v, n, _ := s.Read("k") // writeVer[k] intact: still falls back to R0
	same := reflect.DeepEqual(before, st.View())
	ver, err := s.Write("k", "b")
	noTrace = same && v == "a" && n == 1 && err == nil && ver == 2
	return
}

// bigMOK: routing finds one key among m directly (count pinned in ses tests).
func bigMOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := api.New().Open()
		for i := 0; i < m; i++ {
			s.Write(fmt.Sprintf("k%d", i), "v")
		}
		s.Write("target", "x")
		if v, n, err := s.Read("target"); err != nil || v != "x" || n != 1 {
			return false
		}
	}
	return true
}

func concOK() bool {
	st := api.New()
	s := st.Open()
	for i := 0; i < 20; i++ {
		s.Write(fmt.Sprintf("k%d", i), "v")
	}
	st.Sync(1)
	want := st.View()
	start := make(chan struct{})
	views := make([][3]map[string]kv.Entry, 16)
	var wg sync.WaitGroup
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; s.Read("k3"); views[i] = st.View() }(i)
	}
	close(start)
	wg.Wait()
	return !slices.ContainsFunc(views, func(v [3]map[string]kv.Entry) bool {
		return !reflect.DeepEqual(v, want)
	})
}

func main() {
	eOK, rOK := faultsOK()
	check("七步轨迹与三副本快照", traceOK())
	check("View 与朴素参照一致", viewRefOK())
	check("三类可判定错误互不相同", eOK)
	check("被拒后状态不变可继续用", rOK)
	check("大 m 路由按 key 直接定位", bigMOK())
	check("并发只读 View 逐字段一致", concOK())
	if fails > 0 {
		os.Exit(1)
	}
}
