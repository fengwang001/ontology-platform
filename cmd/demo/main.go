// Command demo 演示排序归并连接的外部排序溢写。
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/mrg"
	"ontology/run"
)

func verdict(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}

func ks(ks []run.Key) string { return fmt.Sprint(ks) }

// buggyJoin 模拟错误实现：le=true 时相等也推进 R（跳过配对）；
// le=false 时相等只配一对、两游标各进一格（不做整段笛卡尔积）。
func buggyJoin(r, s []run.Key, le bool) int {
	n := 0
	i, j := 0, 0
	for i < len(r) && j < len(s) {
		switch {
		case r[i] < s[j]:
			i++
		case r[i] > s[j]:
			j++
		case le:
			i++
		default:
			n++
			i++
			j++
		}
	}
	return n
}

func main() {
	rR, _ := run.MakeRuns([]run.Key{5, 1, 3, 7, 3, 2}, 3)
	rS, _ := run.MakeRuns([]run.Key{3, 6, 3, 2}, 3)
	mR, _ := mrg.Merge(rR, 2)
	mS, _ := mrg.Merge(rS, 2)
	verdict("run结构 R[1 3 5][2 3 7] S[3 3 6][2]；归并 R'[1 2 3 3 5 7] S'[2 3 3 6]",
		ks(rR[0].Keys) == "[1 3 5]" && ks(rR[1].Keys) == "[2 3 7]" &&
			ks(rS[0].Keys) == "[3 3 6]" && ks(rS[1].Keys) == "[2]" &&
			ks(mR) == "[1 2 3 3 5 7]" && ks(mS) == "[2 3 3 6]")

	pairs, steps, _ := mrg.Join(rR, rS, 2)
	outs := make([]string, 6)
	for i, st := range steps {
		if len(st.Out) == 0 {
			outs[i] = "无"
		} else {
			outs[i] = fmt.Sprintf("(%d,%d)x%d", st.Out[0].RKey, st.Out[0].SKey, len(st.Out))
		}
	}
	fmt.Println("六步输出: " + strings.Join(outs, " | "))
	verdict("第2步1对 第3步4对 总5对",
		len(steps[1].Out) == 1 && len(steps[2].Out) == 4 && len(pairs) == 5)

	_, eM := api.New(0, 2)
	_, eF := api.New(3, 1)
	eng, _ := api.New(3, 10000)
	eng.BuildR([]run.Key{5, 1, 3, 7, 3, 2})
	eng.BuildS([]run.Key{3, 6, 3, 2})
	before, _ := eng.Join()
	err := eng.BuildR([]run.Key{1, -2})
	after, _ := eng.Join()
	distinct := errors.Is(eM, api.ErrBadThreshold) && errors.Is(eF, api.ErrBadFanIn) &&
		errors.Is(err, api.ErrNegativeKey) && eM != eF && eF != err && eM != err
	verdict("三类可判定错误且互不相同", distinct)
	verdict("(甲)只配1对=3条 (乙)<=推进=0条 (丙)漏run1=2条",
		buggyJoin(mR, mS, false) == 3 && buggyJoin(mR, mS, true) == 0 &&
			len(mustP(mrg.Join(rR[:1], rS, 2))) == 2)
	verdict("与朴素嵌套循环一致；非法批次被拒后状态不变",
		eng.SelfCheck() == nil && len(after) == len(before))
	verdict("大m(100/1k/1w)取最小检查个数≤2⌈log₂m⌉+2", mrg.ProbeBoundHolds())

	const n = 16
	var wg sync.WaitGroup
	res := make([]string, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ps, _ := eng.Join()
			res[g] = fmt.Sprint(ps) // 归并连接输出顺序确定，直接比字符串多重集
		}(g)
	}
	wg.Wait()
	same := true
	for _, v := range res[1:] {
		if v != res[0] {
			same = false
		}
	}
	verdict("16 goroutine 并发只读 Join 多重集完全一致", same)
}

func mustP(ps []mrg.Pair, _ []mrg.Step, err error) []mrg.Pair {
	if err != nil {
		panic(err)
	}
	return ps
}
