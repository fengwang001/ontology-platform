// demo 逐项演练 join 包的语义并打印 OK/FAIL 判定，最后输出总计。
// 不读命令行参数、不联网，退出码恒为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"ontology/join"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	mark := "OK  "
	if ok {
		passed++
	} else {
		mark = "FAIL"
	}
	fmt.Printf("%s %s: %s\n", mark, name, detail)
}

func main() {
	nilSemantics()
	unmatchedCounts()
	expansion()
	shuffleDeterminism()
	typeConflict()
	numericRules()
	nameCollision()
	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
}

func nilSemantics() {
	left := []join.Row{{"id": nil, "v": "l"}, {"id": 1, "v": "l1"}}
	right := []join.Row{{"id": nil, "v": "r"}, {"id": 1, "v": "r1"}}
	in, err := join.Join(left, right, []string{"id"}, join.Inner)
	lf, err2 := join.Join(left, right, []string{"id"}, join.Left)
	ok := err == nil && err2 == nil && len(in.Rows) == 1 && len(lf.Rows) == 2 &&
		lf.Stats.LeftUnmatchedNull == 1
	check("nil-key", ok, "Inner 下两侧 nil 键不匹配(1 行), Left 下左行保留(2 行)")
}

func unmatchedCounts() {
	left := []join.Row{{"id": nil}, {"k": 1}, {"id": 7}, {"id": 1}}
	right := []join.Row{{"id": 1}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	ok := err == nil && res.Stats.LeftUnmatchedNull == 2 &&
		res.Stats.LeftUnmatchedNoMatch == 1 && res.Stats.MatchedPairs == 1
	check("unmatched-counts", ok, fmt.Sprintf("空键未匹配=%d 有值无匹配=%d 匹配对=%d",
		res.Stats.LeftUnmatchedNull, res.Stats.LeftUnmatchedNoMatch, res.Stats.MatchedPairs))
}

func expansion() {
	left := []join.Row{{"id": 1, "v": "a"}, {"id": 1, "v": "b"}, {"id": 1, "v": "c"}}
	right := []join.Row{{"id": 1, "w": "x"}, {"id": 1, "w": "y"}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	ok := err == nil && len(res.Rows) == 6 && res.Stats.MaxKeyExpansion == 6
	check("expand-3x2", ok, fmt.Sprintf("3x2 重复键展开为 %d 行", len(res.Rows)))
}

func shuffleDeterminism() {
	rng := rand.New(rand.NewSource(1))
	var left, right []join.Row
	for i := 0; i < 200; i++ {
		left = append(left, join.Row{"id": i % 30, "v": fmt.Sprintf("l%d", i)})
		right = append(right, join.Row{"id": (i * 7) % 30, "w": fmt.Sprintf("r%d", i)})
	}
	base, err := join.Join(left, right, []string{"id"}, join.Left)
	rng.Shuffle(len(left), func(i, j int) { left[i], left[j] = left[j], left[i] })
	rng.Shuffle(len(right), func(i, j int) { right[i], right[j] = right[j], right[i] })
	got, err2 := join.Join(left, right, []string{"id"}, join.Left)
	ok := err == nil && err2 == nil && reflect.DeepEqual(base.Rows, got.Rows)
	check("shuffle-determinism", ok, fmt.Sprintf("打乱后结果逐元素一致(%d 行)", len(base.Rows)))
}

func typeConflict() {
	_, err := join.Join(
		[]join.Row{{"id": "s"}}, []join.Row{{"id": int64(1)}}, []string{"id"}, join.Inner)
	ok := errors.Is(err, join.ErrKeyTypeConflict)
	var kte *join.KeyTypeError
	if errors.As(err, &kte) {
		ok = ok && kte.Key == "id" && kte.LeftType == "string" && kte.RightType == "int64"
	}
	check("type-conflict", ok, fmt.Sprintf("string vs int64 报错: %v", err))
}

func numericRules() {
	r1, e1 := join.Join([]join.Row{{"id": int64(42)}}, []join.Row{{"id": 42.0}}, []string{"id"}, join.Inner)
	r2, e2 := join.Join([]join.Row{{"id": math.NaN()}}, []join.Row{{"id": math.NaN()}}, []string{"id"}, join.Inner)
	r3, e3 := join.Join([]join.Row{{"id": 0.0}}, []join.Row{{"id": math.Copysign(0, -1)}}, []string{"id"}, join.Inner)
	ok := e1 == nil && e2 == nil && e3 == nil &&
		len(r1.Rows) == 1 && len(r2.Rows) == 0 && len(r3.Rows) == 1
	check("numeric-rules", ok, "int64==float64, NaN 永不相等, +0.0==-0.0")
}

func nameCollision() {
	left := []join.Row{{"id": 1, "name": "left"}}
	right := []join.Row{{"id": 1, "name": "right"}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	ok := err == nil && len(res.Rows) == 1 &&
		res.Rows[0]["name"] == "left" && res.Rows[0]["right.name"] == "right"
	check("name-collision", ok, fmt.Sprintf("name=%v right.name=%v 未被覆盖",
		res.Rows[0]["name"], res.Rows[0]["right.name"]))
}
