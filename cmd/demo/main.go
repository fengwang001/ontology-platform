package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/gph"
	"ontology/rpl"
)

var failed bool

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	// gph 包判定：暂存→依赖登记齐后激活；激活引发的环被拒绝且不留痕。
	g := gph.New(16)
	g.Commit(3, []int{5}) // 5 未登记 -> 3 暂存
	stagedBefore := len(g.Staged()) == 1
	g.Commit(5, nil) // 3 的依赖登记齐 -> 自动激活
	ok := stagedBefore && len(g.Staged()) == 0

	g2 := gph.New(16)
	g2.Commit(2, []int{1})           // 2 暂存
	_, err := g2.Commit(1, []int{2}) // 1 登记将激活 2，1<->2 成环 -> 整体拒绝
	ok = ok && errors.Is(err, gph.ErrCycle) && len(g2.Staged()) == 1
	report("gph stage/activate/cycle", ok)

	// rpl 包判定：八步序列的三次 Replay 返回 [5 1 3] / [2] / [7]。
	r := rpl.New(gph.New(16))
	r.Commit(3, []int{5})
	r.Commit(5, nil)
	r.Commit(1, []int{5})
	seq1 := r.Replay()
	r.Commit(2, []int{1})
	seq2 := r.Replay()
	r.Commit(7, []int{2})
	seq3 := r.Replay()
	report("rpl replay order", reflect.DeepEqual(seq1, []int{5, 1, 3}) &&
		reflect.DeepEqual(seq2, []int{2}) && reflect.DeepEqual(seq3, []int{7}))

	if failed {
		os.Exit(1)
	}
}
