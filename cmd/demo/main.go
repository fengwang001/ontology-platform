// demo 演示带允许迟到的迟到丢弃与更新：逐条打印 OK/FAIL，全 OK 退出码 0。
package main

import (
	"fmt"
	"os"
	"reflect"

	"ontology/wagg"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
	}
	mark := "OK"
	if !cond {
		mark = "FAIL"
	}
	fmt.Printf("%s: %s\n", name, mark)
}

// 第三节八步：size=10, delay=3, lateness=5，TS=2,9,15,4,18,5,22,7。
func checkSteps() {
	a := wagg.New(10, 3, 5, 0)
	ts := []int64{2, 9, 15, 4, 18, 5, 22, 7}
	wantWM := []int64{-1, 6, 12, 12, 15, 15, 19, 19}
	wantOut := [][]wagg.Change{
		{}, {},
		{{false, "K", 10, 20, 1}, {false, "K", 0, 10, 2}},
		{{true, "K", 0, 10, 2}, {false, "K", 0, 10, 3}},
		{}, {}, {}, {},
	}
	good := true
	for i, t := range ts {
		out, err := a.Feed([]wagg.Event{{Key: "K", TS: t}})
		wm, _ := a.WM()
		if err != nil || wm != wantWM[i] || !reflect.DeepEqual(out, wantOut[i]) {
			good = false
		}
	}
	ok("steps 1-8 (wm+output)", good && a.Dropped() == 2)
}

func main() {
	checkSteps()
	if failed {
		os.Exit(1)
	}
}
