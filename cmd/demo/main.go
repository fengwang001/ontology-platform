package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/ddl"
	"ontology/phase"
)

var failed bool

func report(name string, ok bool, detail string) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Printf("%s: %s %s\n", name, s, detail)
}

// lastChecked 反射读取 ddl 的非导出计数器（公开接口本就不暴露它）。
func lastChecked(s *ddl.Store) int {
	f := reflect.ValueOf(s).Elem().FieldByName("lastChecked")
	return int(reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int())
}

func main() {
	st := ddl.New(1000)
	ops := []func() error{
		func() error { return st.Put("a", 3) }, func() error { return st.Put("b", 5) },
		st.BeginMigration, func() error { return st.Put("c", 4) },
		st.Backfill, st.Backfill, func() error { return st.Put("a", 7) },
		st.Switch, func() error { return st.Put("d", 2) },
	}
	want := []struct {
		ph     phase.Phase
		v1, v2 map[string]int
	}{
		{phase.Normal, map[string]int{"a": 3}, map[string]int{}},
		{phase.Normal, map[string]int{"a": 3, "b": 5}, map[string]int{}},
		{phase.DualWrite, map[string]int{"a": 3, "b": 5}, map[string]int{}},
		{phase.DualWrite, map[string]int{"a": 3, "b": 5, "c": 4}, map[string]int{"c": 8}},
		{phase.DualWrite, map[string]int{"a": 3, "b": 5, "c": 4}, map[string]int{"a": 6, "b": 10, "c": 8}},
		{phase.DualWrite, map[string]int{"a": 3, "b": 5, "c": 4}, map[string]int{"a": 6, "b": 10, "c": 8}},
		{phase.DualWrite, map[string]int{"a": 7, "b": 5, "c": 4}, map[string]int{"a": 14, "b": 10, "c": 8}},
		{phase.Switched, map[string]int{"a": 7, "b": 5, "c": 4}, map[string]int{"a": 14, "b": 10, "c": 8}},
		{phase.Switched, map[string]int{"a": 7, "b": 5, "c": 4}, map[string]int{"a": 14, "b": 10, "c": 8, "d": 4}},
	}
	names, traceOK := [...]string{"N", "DW", "SW"}, true
	seg, gets := make([]string, 0, 9), make([]int, 0, 9)
	for i, op := range ops {
		op()
		ph, v1, v2 := st.Snapshot()
		if ph != want[i].ph || !reflect.DeepEqual(v1, want[i].v1) || !reflect.DeepEqual(v2, want[i].v2) {
			traceOK = false
		}
		seg = append(seg, fmt.Sprintf("%d:%s%v/%v", i+1, names[ph], v1, v2))
		gets = append(gets, st.Get("a"))
	}
	report("trace 1-5", traceOK, fmt.Sprint(seg[:5]))
	report("trace 6-9", traceOK, fmt.Sprint(seg[5:]))
	report("switch read", gets[6] == 7 && gets[7] == 14, fmt.Sprintf("before=%d after=%d", gets[6], gets[7]))
	var base []int // 回填幂等：回填 1/2/3 次后切换，读值一致
	idem := true
	for n := 1; n <= 3; n++ {
		t := api.New(1000)
		t.Put("a", 3)
		t.Put("b", 5)
		t.BeginMigration()
		for i := 0; i < n; i++ {
			t.Backfill()
		}
		t.Switch()
		if got := []int{t.Get("a"), t.Get("b")}; base == nil {
			base = got
		} else if !reflect.DeepEqual(base, got) {
			idem = false
		}
	}
	report("backfill idempotent", idem, fmt.Sprint(base))
	d := api.New(1000) // 双写原子：故障注入后 Put 整体失败，v1 也不写
	d.Put("a", 3)
	d.BeginMigration()
	d.InjectDualWriteFault()
	err := d.Put("c", 1)
	report("dual-write atomic", errors.Is(err, api.ErrDualWrite) && d.Get("c") == 0, fmt.Sprintf("err=%v", err))
	bg, fw := api.New(10), api.New(10) // 四类可判定错误，互不相同
	bg.BeginMigration()
	fw.BeginMigration()
	fw.InjectDualWriteFault()
	errs := []error{bg.BeginMigration(), api.New(10).Put("", 1), api.New(10).Put("x", -1), fw.Put("z", 1)}
	sents := []error{api.ErrPhase, api.ErrKey, api.ErrValue, api.ErrDualWrite}
	seen := map[error]bool{}
	distinct := true
	for i, e := range errs {
		seen[sents[i]] = true
		distinct = distinct && errors.Is(e, sents[i])
	}
	report("four errors distinct", distinct && len(seen) == 4, fmt.Sprintf("%v", errs))
	r := api.New(10) // 被拒后状态不变且可继续使用
	r.Put("a", 3)
	r.Put("", 1)
	r.Put("a", -1)
	r.Switch()
	r.Backfill()
	report("rejected no trace", r.Get("a") == 3 && r.Put("b", 2) == nil, "a still 3")
	c := ddl.New(1 << 20) // 第二次回填检查个数为 0
	for i := 0; i < 100; i++ {
		c.Put(fmt.Sprintf("k%d", i), i+1)
	}
	c.BeginMigration()
	c.Backfill()
	n1 := lastChecked(c)
	c.Backfill()
	report("second backfill checks", n1 == 100 && lastChecked(c) == 0, fmt.Sprintf("first=%d second=%d", n1, lastChecked(c)))
	g := api.New(1 << 20) // 并发读一致 + 并发写不同键
	g.Put("hot", 7)
	const n = 8
	var wg sync.WaitGroup
	start, bad := make(chan struct{}), make(chan string, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if g.Get("hot") != 7 {
				bad <- "get"
			}
			if g.Put(fmt.Sprintf("k%d", i), i+1) != nil {
				bad <- "put"
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(bad)
	concOK := len(bad) == 0
	for i := 0; i < n; i++ {
		concOK = concOK && g.Get(fmt.Sprintf("k%d", i)) == i+1
	}
	report("concurrent read", concOK, fmt.Sprintf("%d goroutines", n))
	if failed {
		os.Exit(1)
	}
}
