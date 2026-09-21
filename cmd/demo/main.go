// demo 逐项演练带阈值编辑距离匹配器的行为，每行打印 OK/FAIL 判定。
// 不读命令行参数、不联网，退出码恒为 0。
package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/editdist"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %-24s %s\n", status, name, detail)
}

func main() {
	r1, _ := editdist.Distance("kitten", "sitting", 5)
	f1, _ := editdist.Full("kitten", "sitting", editdist.Options{})
	check("exact==full within k", !r1.Exceeded && r1.Distance == f1.Distance,
		fmt.Sprintf("k=5 d=%d full=%d", r1.Distance, f1.Distance))

	r2, _ := editdist.Distance(strings.Repeat("a", 1000), strings.Repeat("b", 1000), 2)
	check("1000x1000 k=2 banded", r2.Exceeded && r2.Stats.CellsFilled <= 5*1001,
		fmt.Sprintf("cells=%d bound=%d grid=1000000", r2.Stats.CellsFilled, 5*1001))

	r3, _ := editdist.Distance("abc", "abcdefgh", 2)
	check("len diff>k zero cells", r3.Exceeded && r3.Stats.CellsFilled == 0,
		fmt.Sprintf("cells=%d", r3.Stats.CellsFilled))

	r4, _ := editdist.Distance("café", "cafe", 1)
	check("café vs cafe == 1", !r4.Exceeded && r4.Distance == 1, fmt.Sprintf("d=%d", r4.Distance))

	r5, _ := editdist.Distance("a🙂b", "ab", 1)
	check("emoji insert == 1", !r5.Exceeded && r5.Distance == 1, fmt.Sprintf("d=%d", r5.Distance))

	_, err := editdist.Distance(string([]byte{'a', 'b', 0xff, 'c'}), "abc", 3)
	var uerr *editdist.UTF8Error
	check("invalid utf8 located", errors.As(err, &uerr) && uerr.Side == "a" && uerr.Offset == 2,
		fmt.Sprintf("err=%q", err))

	_, errNeg := editdist.Distance("a", "a", -1)
	rEq, _ := editdist.Distance("same", "same", 0)
	rNeq, _ := editdist.Distance("same", "diff", 0)
	check("k<0 err, k=0 equality", errors.Is(errNeg, editdist.ErrNegativeK) &&
		!rEq.Exceeded && rEq.Distance == 0 && rNeq.Exceeded, "neg=ErrNegativeK eq=0 neq=exceeded")

	x, _ := editdist.Distance("abcdef", "azced", 3)
	y, _ := editdist.Distance("azced", "abcdef", 3)
	check("symmetry incl. cells", x.Distance == y.Distance && x.Stats.CellsFilled == y.Stats.CellsFilled,
		fmt.Sprintf("d=%d cells=%d", x.Distance, x.Stats.CellsFilled))

	rf, _ := editdist.Within("Hello", "hELLO", 0, editdist.Options{CaseFold: true})
	check("case fold option", !rf.Exceeded && rf.Distance == 0, "Hello==hELLO with fold")

	big := strings.Repeat("x", 100000)
	rBig, _ := editdist.Distance(big, big[:99997]+"abc", 3)
	check("100k runes work array", !rBig.Exceeded && rBig.Distance == 3 && rBig.Stats.MaxWorkLen <= 7,
		fmt.Sprintf("maxWorkLen=%d", rBig.Stats.MaxWorkLen))

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
}
