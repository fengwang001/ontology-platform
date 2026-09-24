// Command demo 逐条判定版本向量冲突检测器的关键性质。
package main

import (
	"errors"
	"fmt"

	"ontology/vv"
)

var total, fails int

func check(name string, ok bool) {
	total++
	if ok {
		fmt.Println("OK   " + name)
		return
	}
	fails++
	fmt.Println("FAIL " + name)
}

func vvChecks() {
	check("relation equal", vv.Compare(vv.Vector{"A": 1}, vv.Vector{"A": 1}) == vv.Equal)
	check("relation less", vv.Compare(vv.Vector{"A": 1}, vv.Vector{"A": 2}) == vv.Less)
	check("relation greater", vv.Compare(vv.Vector{"A": 2}, vv.Vector{"A": 1}) == vv.Greater)
	check("relation concurrent",
		vv.Compare(vv.Vector{"A": 1, "B": 0}, vv.Vector{"A": 0, "B": 1}) == vv.Concurrent)
	check("missing component is zero",
		vv.Compare(vv.Vector{"A": 1}, vv.Vector{"A": 1, "B": 0}) == vv.Equal)
	a, b := vv.Vector{"A": 1, "B": 2, "C": 3}, vv.Vector{"B": 5, "C": 1, "D": 9}
	_, n := vv.CompareCounted(a, b)
	check("compare reads <= 2*union", n == 4 && n <= 2*4)
	raw := vv.Vector{"A": 1, "B": 2}.Encode()
	_, hErr := vv.Decode(raw[:2])
	_, eErr := vv.Decode(raw[:6])
	_, cErr := vv.Decode(raw[:len(raw)-2])
	check("truncation header/component/crc",
		errors.Is(hErr, vv.ErrHeader) && errors.Is(eErr, vv.ErrEntry) && errors.Is(cErr, vv.ErrCRC))
}

func main() {
	vvChecks()
	if fails == 0 {
		fmt.Printf("TOTAL %d checks, 0 fail\n", total)
	} else {
		fmt.Printf("TOTAL %d checks, %d FAIL\n", total, fails)
	}
}
