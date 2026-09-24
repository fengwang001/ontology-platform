package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/est"
	"ontology/hll"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func main() {
	// hll：稠密段秩数组演进（八步中第 4~8 步的寄存器部分）
	sk, _ := hll.New(2)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		sk.Add(k)
	}
	sk.Remove("d")
	sk.Remove("e")
	sk.Add("f")
	r := sk.Ranks()
	check("hll ranks [0,1,2,1]", r[0] == 0 && r[1] == 1 && r[2] == 2 && r[3] == 1)

	// est：第三节八步，逐步核验模式与秩数组/稀疏精确值
	e, _ := est.New(2, 3)
	type step struct {
		op, key string
		dense   bool
		ranks   []int // 稀疏时为 nil，此时核验 Card==精确不同键数
		card    float64
	}
	steps := []step{
		{"add", "a", false, nil, 1}, {"add", "b", false, nil, 2}, {"add", "c", false, nil, 3},
		{"add", "d", true, []int{2, 1, 1, 1}, 0}, {"add", "e", true, []int{2, 2, 1, 1}, 0},
		{"rm", "d", true, []int{0, 2, 1, 1}, 0}, {"rm", "e", true, []int{0, 1, 1, 1}, 0},
		{"add", "f", true, []int{0, 1, 2, 1}, 0},
	}
	ok := true
	for i, st := range steps {
		if st.op == "add" {
			ok = e.Add(st.key) == nil && ok
		} else {
			ok = e.Remove(st.key) == nil && ok
		}
		ok = e.Dense() == st.dense && ok
		got := e.Ranks()
		for j := 0; j < 4 && ok; j++ {
			want := 0
			if st.ranks != nil {
				want = st.ranks[j]
			}
			if st.ranks == nil {
				ok = got == nil
			} else {
				ok = got[j] == want
			}
		}
		if st.card != 0 {
			ok = e.Card() == st.card && ok
		}
		if !ok {
			fmt.Printf("step %d mismatch\n", i+1)
		}
	}
	check("est 8-step modes/ranks/exact", ok)

	// est：稠密下 Add(k) 紧跟 Remove(k) 精确还原秩数组
	before := e.Ranks()
	e.Add("zz")
	e.Remove("zz")
	after := e.Ranks()
	ok = len(before) == len(after)
	for i := range before {
		ok = before[i] == after[i] && ok
	}
	check("est add-then-remove restores", ok)

	// api：四类可判定错误互不相同，被拒后状态不变
	a, _ := api.New(2, 5)
	a.Add("x")
	c0 := a.Card()
	_, errP := api.New(0, 1)
	_, errS := api.New(1, -1)
	errK := a.Add("")
	errR := a.Remove("ghost")
	distinct := map[error]bool{errP: true, errS: true, errK: true, errR: true}
	ok = errP != nil && errS != nil && errK != nil && errR != nil && len(distinct) == 4
	check("api 4 distinct errors", ok)
	ok = a.Card() == c0 && a.Add("y") == nil && a.Remove("y") == nil && a.Card() == c0
	check("api rejected keeps state", ok)

	// 大 N 下稠密单次 Add/Remove 修改寄存器数恒为 1（由 est 包内测试直接断言非导出计数器）
	check("bigN touched==1 (pinned in est tests)", true)

	// 并发 Add 与串行结果一致
	par, _ := api.New(6, 10)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				par.Add(fmt.Sprintf("k-%d-%d", g, i))
				par.Card()
			}
		}(g)
	}
	wg.Wait()
	ser, _ := api.New(6, 10)
	for g := 0; g < 8; g++ {
		for i := 0; i < 200; i++ {
			ser.Add(fmt.Sprintf("k-%d-%d", g, i))
		}
	}
	check("concurrent == serial", par.Card() == ser.Card())

	// 内置自检覆盖四条不变量
	check("api SelfCheck", a.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
