package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/est"
	"ontology/hll"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK " + name)
}

// ranksOf 返回秩数组的紧凑字符串，如 [0,1,2,1]。
func ranksOf(r []int) string {
	s := "["
	for i, v := range r {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprint(v)
	}
	return s + "]"
}

// touched 返回执行 op 前后秩数组发生变化的寄存器个数。
func touched(sk *hll.Sketch, op func()) int {
	before := sk.Ranks()
	op()
	after := sk.Ranks()
	n := 0
	for i := range before {
		if before[i] != after[i] {
			n++
		}
	}
	return n
}

func main() {
	// est：第三节八步，逐步核验模式与秩数组（稀疏集合由 demo 自行记账）。
	e, _ := est.New(2, 3)
	type step struct {
		op, key, want string
	}
	steps := []step{
		{"add", "a", "S{a}"}, {"add", "b", "S{a,b}"}, {"add", "c", "S{a,b,c}"},
		{"add", "d", "D[2,1,1,1]"}, {"add", "e", "D[2,2,1,1]"},
		{"rem", "d", "D[0,2,1,1]"}, {"rem", "e", "D[0,1,1,1]"}, {"add", "f", "D[0,1,2,1]"},
	}
	set := map[string]bool{}
	got := ""
	for _, st := range steps {
		if st.op == "add" {
			_ = e.Add(st.key)
			set[st.key] = true
		} else {
			_ = e.Remove(st.key)
			delete(set, st.key)
		}
		if e.Dense() {
			got += " D" + ranksOf(e.Ranks())
		} else {
			got += " S"
		}
	}
	wantTable := " S S S D[2,1,1,1] D[2,2,1,1] D[0,2,1,1] D[0,1,1,1] D[0,1,2,1]"
	check("eight-step modes+ranks"+got, got == wantTable && len(set) == 4)

	// est：稀疏精确计数 + 恰在 d==S+1 转稠密（第 3 个键时仍稀疏、Card==3）。
	e2, _ := est.New(2, 3)
	_ = e2.Add("a")
	_ = e2.Add("b")
	_ = e2.Add("c")
	sparseExact := !e2.Dense() && e2.Card() == 3
	_ = e2.Add("d")
	check("sparse exact=3, dense exactly at d==S+1", sparseExact && e2.Dense())

	// est：稠密下 Add(k) 紧跟 Remove(k)，秩数组精确还原。
	e3, _ := est.New(2, 0)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_ = e3.Add(k)
	}
	snap := ranksOf(e3.Ranks())
	_ = e3.Add("zz-new")
	_ = e3.Remove("zz-new")
	check("add-then-remove restores ranks "+snap, ranksOf(e3.Ranks()) == snap)

	// est：四类可判定错误互不相同；被拒后状态不变，仍可正常使用。
	e4, _ := est.New(2, 1)
	_ = e4.Add("a")
	_ = e4.Add("b") // 转稠密
	cBefore, rBefore := e4.Card(), ranksOf(e4.Ranks())
	_, errP := est.New(0, 1)
	_, errS := est.New(1, -1)
	errK := e4.Add("")
	errR := e4.Remove("ghost")
	sents := []error{est.ErrInvalidP, est.ErrInvalidS, est.ErrEmptyKey, est.ErrNotPresent}
	errs := []error{errP, errS, errK, errR}
	distinct := true
	for i := range sents {
		distinct = distinct && errors.Is(errs[i], sents[i])
		for j := range sents {
			if i != j {
				distinct = distinct && !errors.Is(errs[i], sents[j])
			}
		}
	}
	check("four distinct decidable errors", distinct)
	unchanged := e4.Card() == cBefore && ranksOf(e4.Ranks()) == rBefore
	check("rejected ops leave state unchanged, still usable",
		unchanged && e4.Add("c") == nil)

	// hll：删除最大秩后回落到次大秩（e 秩2 删后 a 秩1 仍在）。
	sk, _ := hll.New(2)
	sk.Add("a")
	sk.Add("e")
	before := ranksOf(sk.Ranks())
	_ = sk.Remove("e")
	check("hll remove-max falls to second-max "+before+"->"+ranksOf(sk.Ranks()),
		before == "[0,2,0,0]" && ranksOf(sk.Ranks()) == "[0,1,0,0]")

	// hll：大 N 下单次 Add/Remove 只改一个寄存器（不随 N 增长）。
	big, _ := hll.New(10)
	for i := 0; i < 10000; i++ {
		big.Add(fmt.Sprintf("key-%d", i))
	}
	nAdd := touched(big, func() { big.Add("one-more-key") })
	nRem := touched(big, func() { _ = big.Remove("one-more-key") })
	check(fmt.Sprintf("hll large-N single register touch add=%d remove=%d", nAdd, nRem),
		nAdd == 1 && nRem == 1)

	if failed {
		os.Exit(1)
	}
}
