// demo 实际演练 ontology/join 两表等值连接器的各项语义，
// 逐条打印 OK/FAIL 判定，最后打印总计。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"

	"ontology/join"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s: %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s: %s\n", name, detail)
	}
}

func reversed(rows []join.Row) []join.Row {
	out := make([]join.Row, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = r
	}
	return out
}

func main() {
	// 1. 两侧 nil 键：Inner 不匹配，Left 保留左行。
	nilL := []join.Row{{"id": nil, "lv": "L"}}
	nilR := []join.Row{{"id": nil, "rv": "R"}}
	in, _ := join.Join(nilL, nilR, []string{"id"}, join.Inner)
	lf, _ := join.Join(nilL, nilR, []string{"id"}, join.Left)
	check("nil-key", len(in.Rows) == 0 && len(lf.Rows) == 1 && !join.Has(lf.Rows[0], "rv"),
		fmt.Sprintf("inner=%d行 left=%d行且右侧缺失", len(in.Rows), len(lf.Rows)))

	// 2. 两类未匹配计数分开。
	left := []join.Row{{"id": nil}, {"id": int64(1)}, {"id": int64(2)}}
	right := []join.Row{{"id": int64(2)}}
	res, _ := join.Join(left, right, []string{"id"}, join.Left)
	s := res.Stats
	check("unmatched-counts", s.LeftNullKeyRows == 1 && s.LeftUnmatchedRows == 1,
		fmt.Sprintf("空键未匹配=%d 有值未匹配=%d", s.LeftNullKeyRows, s.LeftUnmatchedRows))

	// 3. 3x2 重复键展开为 6 行。
	l3 := []join.Row{{"k": "a", "i": 1}, {"k": "a", "i": 2}, {"k": "a", "i": 3}}
	r2 := []join.Row{{"k": "a", "j": 1}, {"k": "a", "j": 2}}
	exp, _ := join.Join(l3, r2, []string{"k"}, join.Inner)
	check("expand-3x2", exp.Stats.OutputRows == 6 && exp.Stats.MaxKeyExpansion == 6,
		fmt.Sprintf("产出=%d行 最大单键展开=%d", exp.Stats.OutputRows, exp.Stats.MaxKeyExpansion))

	// 4. 左右表各自打乱后结果逐元素一致。
	again, _ := join.Join(reversed(l3), reversed(r2), []string{"k"}, join.Inner)
	check("shuffle-deterministic", reflect.DeepEqual(exp.Rows, again.Rows),
		fmt.Sprintf("原序与乱序各 %d 行完全一致", len(again.Rows)))

	// 5. 连接键类型冲突返回可判定错误。
	_, err := join.Join([]join.Row{{"id": "s"}}, []join.Row{{"id": int64(1)}},
		[]string{"id"}, join.Inner)
	var te *join.TypeError
	check("type-conflict", errors.As(err, &te) && te.Key == "id",
		fmt.Sprintf("err=%v", err))

	// 6. int64 与 float64 数值相等。
	num, _ := join.Join([]join.Row{{"id": int64(2)}}, []join.Row{{"id": 2.0}},
		[]string{"id"}, join.Inner)
	check("int64-eq-float64", num.Stats.MatchedRows == 1,
		fmt.Sprintf("int64(2)==float64(2.0) 匹配=%d", num.Stats.MatchedRows))

	// 7. NaN 永不相等，且计入空键未匹配。
	nan := math.NaN()
	nn, _ := join.Join([]join.Row{{"id": nan}}, []join.Row{{"id": nan}},
		[]string{"id"}, join.Left)
	check("nan-never-equal", nn.Stats.MatchedRows == 0 && nn.Stats.LeftNullKeyRows == 1,
		fmt.Sprintf("匹配=%d 空键未匹配=%d", nn.Stats.MatchedRows, nn.Stats.LeftNullKeyRows))

	// 8. +0.0 与 -0.0 相等。
	zero, _ := join.Join([]join.Row{{"id": 0.0}}, []join.Row{{"id": math.Copysign(0, -1)}},
		[]string{"id"}, join.Inner)
	check("signed-zero-equal", zero.Stats.MatchedRows == 1,
		fmt.Sprintf("+0.0==-0.0 匹配=%d", zero.Stats.MatchedRows))

	// 9. 同名非连接键属性：右表不覆盖左表。
	cl, _ := join.Join(
		[]join.Row{{"id": int64(1), "v": "left"}},
		[]join.Row{{"id": int64(1), "v": "right"}},
		[]string{"id"}, join.Inner)
	row := cl.Rows[0]
	check("no-overwrite", row["v"] == "left" && row["right.v"] == "right",
		fmt.Sprintf("v=%v right.v=%v", row["v"], row["right.v"]))

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
