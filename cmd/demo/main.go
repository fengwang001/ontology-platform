package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/snap"
)

var failed bool

func check(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	// 第三节八步序列：记录每步之后的 ver 与两次读结果。
	st, _ := api.New(32)
	trace := func() string { s, _ := st.Snapshot(); return fmt.Sprint(s) }
	t := ""
	_ = st.Write("a", 10)
	t += trace() + " "
	_ = st.Write("b", 20)
	t += trace() + " "
	s1, _ := st.Snapshot()
	t += trace() + " "
	_ = st.Write("a", 15)
	t += trace() + " "
	r5, _ := st.Read(s1, "a")
	t += trace() + " "
	_ = st.Write("b", 25)
	t += trace() + " "
	r7, _ := st.Read(s1, "b")
	t += trace() + " "
	s2, _ := st.Snapshot()
	t += trace()
	check("seq8 ver=["+t+"] reads=10,20", t == "1 2 2 3 3 4 4 4" && s1 == 2 && s2 == 4 && r5 == 10 && r7 == 20)
	check("step5-old-value-visible", r5 == 10 && st.ReadCurrent("a") == 15)
	check("step7-boundary-le", r7 == 20 && st.ReadCurrent("b") == 25)
	ca, _ := st.Read(s1, "a")
	cb, _ := st.Read(s1, "b")
	check("snapshot-cross-key-consistent", ca == 10 && cb == 20)
	// 四类可判定错误互不相同。
	_, e1 := api.New(0)
	e2 := st.Write("", 1)
	_, e3 := st.Read(-1, "a")
	lim, _ := api.New(1)
	_, _ = lim.Snapshot()
	_, e4 := lim.Snapshot()
	errs := []error{e1, e2, e3, e4}
	distinct := true
	for i := range errs {
		if errs[i] == nil {
			distinct = false
		}
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	check("four-errors-distinct", distinct && errors.Is(e1, api.ErrMaxSnapshots) &&
		errors.Is(e2, api.ErrEmptyKey) && errors.Is(e3, api.ErrInvalidSnapshot) &&
		errors.Is(e4, api.ErrTooManySnapshots))
	// 被拒后状态不变。
	before, _ := st.Snapshot()
	_, _ = st.Read(-1, "a")
	_ = st.Write("", 9)
	_ = st.Release(999)
	after, _ := st.Snapshot()
	v, _ := st.Read(s1, "a")
	check("rejected-ops-leave-no-trace", before == after && v == 10)
	check("read-checks-sublinear", snap.SelfCheck() == nil)
	// 并发：快照后 N 个 goroutine 各写一个 Key，快照读全为 0，当前读为各自值。
	c, _ := api.New(4)
	cs, _ := c.Snapshot()
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = c.Write(fmt.Sprintf("k%d", i), int64(i+1))
		}(i)
	}
	wg.Wait()
	conOK := true
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k%d", i)
		if v, err := c.Read(cs, k); err != nil || v != 0 || c.ReadCurrent(k) != int64(i+1) {
			conOK = false
		}
	}
	check("concurrent-writes-snapshot-read", conOK)
	sc, _ := api.New(8)
	check("selfcheck", sc.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
