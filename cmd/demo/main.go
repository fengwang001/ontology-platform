package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"ontology/internal/api"
	"ontology/internal/route"
	"ontology/internal/shard"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		failed = true
		fmt.Println("FAIL", name)
	}
}

// naiveRebuild 把全部 key 按当前 n 重新哈希分桶（不变量 2 的参照系）。
func naiveRebuild(kv map[string]string, n int) []map[string]string {
	out := make([]map[string]string, n)
	for i := range out {
		out[i] = map[string]string{}
	}
	for k, v := range kv {
		out[route.Home(k, n)][k] = v
	}
	return out
}

// route 判定：五行迁移方向（a:留1 b:0→2 c:1→0 d:0→1 e:1→2）。
func routeChecks() {
	want := map[byte][2]int{'a': {1, 1}, 'b': {0, 2}, 'c': {1, 0}, 'd': {0, 1}, 'e': {1, 2}}
	ok := true
	for k, w := range want {
		if route.Home(string(k), 2) != w[0] || route.Home(string(k), 3) != w[1] {
			ok = false
		}
	}
	check("route five-row migration directions", ok)
}

// shard 判定：Rebalance(3) 与朴素重建一致、Get("c")="C"。
func shardChecks() {
	s := shard.New(2)
	kv := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		v := strings.ToUpper(k)
		s.Put(k, v)
		kv[k] = v
	}
	s.Rebalance(3)
	v, found := s.Get("c")
	check("Rebalance(3) equals naive rebuild",
		reflect.DeepEqual(s.Dump(), naiveRebuild(kv, 3)) && found && v == "C")
}

// api 判定：Get/GetPartition、三类互异哨兵、被拒不留痕、SelfCheck。
func apiChecks() {
	st, _ := api.New(2)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_ = st.Put(k, strings.ToUpper(k))
	}
	_ = st.Rebalance(3)
	v, found, _ := st.Get("c")
	r, _ := st.GetPartition(0, "b")
	check(`Get(c)="C"; GetPartition(0,"b") movedTo=2`, found && v == "C" && r.Moved && r.MovedTo == 2)

	_, eNew := api.New(-1)
	_, eRange := st.GetPartition(7, "a")
	eKey := st.Put("", "X")
	distinct := errors.Is(eNew, api.ErrInvalidN) && errors.Is(eRange, api.ErrPartitionOutOfRange) &&
		errors.Is(eKey, api.ErrEmptyKey) && api.ErrInvalidN != api.ErrPartitionOutOfRange &&
		api.ErrPartitionOutOfRange != api.ErrEmptyKey
	before := st.Dump()
	_ = st.Rebalance(0)
	_, _, _ = st.Get("")
	_ = st.Put("", "z")
	_, _ = st.GetPartition(-1, "a")
	check("three distinct sentinel errors", distinct)
	check("state unchanged after rejected ops", reflect.DeepEqual(before, st.Dump()))
	check("SelfCheck all four invariants", st.SelfCheck() == nil)
}

// scale 判定：m=100..10000 始终算术单跳（probes 恒 1 由白盒 TestProbeCountConstant 钉住）。
func scaleChecks() {
	ok := true
	for _, m := range []int{100, 1000, 10000} {
		st, _ := api.New(m)
		_ = st.Put("k", "V")
		home := route.Home("k", m)
		at, _ := st.GetPartition(home, "k")
		away, _ := st.GetPartition((home+1)%m, "k")
		if !at.Found || at.Value != "V" || !away.Moved || away.MovedTo != home {
			ok = false
		}
	}
	check("single-step routing across m=100..10000", ok)
}

// concurrency 判定：多 goroutine 只读结果逐字段相同（无 sleep）。
func concurrencyChecks() {
	st, _ := api.New(2)
	_ = st.Put("b", "B")
	_ = st.Rebalance(3)
	const g = 16
	sigs := make([]string, g)
	var wg sync.WaitGroup
	wg.Add(g)
	for i := 0; i < g; i++ {
		go func(i int) {
			defer wg.Done()
			r, _ := st.GetPartition(0, "b")
			v, f, _ := st.Get("b")
			sigs[i] = fmt.Sprintf("%+v %s %v", r, v, f)
		}(i)
	}
	wg.Wait()
	ok := true
	for i := 1; i < g; i++ {
		if sigs[i] != sigs[0] {
			ok = false
		}
	}
	check("concurrent readers see identical results", ok)
}

func main() {
	routeChecks()
	shardChecks()
	apiChecks()
	scaleChecks()
	concurrencyChecks()
	if failed {
		panic("demo checks failed")
	}
}
