// demo 演示多副本反熵读修复：八步序列、冲突裁决、错误判定、并发一致性。
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/rep"
	"ontology/rrep"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// rep 包：版本比较与并列冲突取字典序更大者
	w, idx, ok := rep.Winner([]rep.Entry{
		{Value: "b", Ver: 7},
		{Value: "c", Ver: 7},
		{Empty: true},
	})
	check("rep winner 并列取字典序更大者", ok && idx == 1 && w.Value == "c" && w.Ver == 7)

	// rrep 包：空副本回填与修复收敛
	st := rrep.NewStore(3)
	st.Put("k", "a", 5, []int{0, 1})
	v, rep1, found := st.Read("k")
	_, rep2, _ := st.Read("k")
	snap := st.Snapshot("k")
	check("rrep 空副本回填+收敛", found && v == "a" && rep1 == 1 && rep2 == 0 &&
		snap[0] == snap[1] && snap[1] == snap[2] && snap[2].Ver == 5)

	// api 包：第三节八步序列，逐步核对 winner 与 repaired
	eng, _ := api.New(3)
	steps := []struct {
		put     bool
		v       string
		ver     int64
		reps    []int
		wantV   string
		wantRep int
	}{
		{true, "a", 5, []int{0, 1}, "", 0},
		{false, "", 0, nil, "a", 1},
		{true, "b", 7, []int{1, 2}, "", 0},
		{true, "c", 7, []int{2}, "", 0},
		{false, "", 0, nil, "c", 2},
		{true, "d", 4, []int{0}, "", 0},
		{false, "", 0, nil, "c", 1},
		{false, "", 0, nil, "c", 0},
	}
	seqOK := true
	for _, s := range steps {
		if s.put {
			seqOK = seqOK && eng.Put("k", s.v, s.ver, s.reps) == nil
			continue
		}
		v, f, r, err := eng.Read("k")
		seqOK = seqOK && err == nil && f && v == s.wantV && r == s.wantRep
	}
	check("api 八步序列 winner/repaired", seqOK)

	// api 包：四类可判定错误互不相同，且被拒后状态不变
	e2, _ := api.New(2)
	_ = e2.Put("x", "v", 3, []int{0})
	before := e2.Snapshot("x")
	errs := []error{
		e2.Put("", "a", 1, []int{0}), e2.Put("x", "a", 0, []int{0}),
		e2.Put("x", "a", 1, nil), e2.Put("x", "a", 1, []int{2}),
	}
	errOK := errs[0] == api.ErrEmptyKey && errs[1] == api.ErrBadVer &&
		errs[2] == api.ErrBadReps && errs[3] == api.ErrBadReps
	_, nErr := api.New(0)
	after := e2.Snapshot("x")
	check("api 四类哨兵错误", nErr == api.ErrBadN && errOK)
	check("api 被拒后状态不变", before[0] == after[0] && before[1] == after[1])
	check("api SelfCheck", e2.SelfCheck() == nil)

	// 大 m：最高版本只落在少数副本，结果与回填数正确（比较数 O(1) 由 rrep 内部测试钉住）
	const m = 10000
	big, _ := api.New(m)
	_ = big.Put("k", "low", 1, []int{0, 1})
	_ = big.Put("k", "high", 9, []int{m / 2, m - 1})
	bv, _, br, _ := big.Read("k")
	check("大m读修复正确", bv == "high" && br == m-2)

	// 并发只读结果一致
	ceng, _ := api.New(8)
	_ = ceng.Put("hot", "a", 5, []int{0, 3})
	_ = ceng.Put("hot", "b", 9, []int{2})
	_ = ceng.Put("hot", "c", 9, []int{5})
	cv, _, _, _ := ceng.Read("hot") // 先收敛
	const R = 32
	vals, creps := make([]string, R), make([]int, R)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < R; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, _, r, _ := ceng.Read("hot")
			vals[i], creps[i] = v, r
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := range vals {
		same = same && vals[i] == cv && creps[i] == 0
	}
	check("并发只读结果一致", same)

	if failed {
		os.Exit(1)
	}
}
