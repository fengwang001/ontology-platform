// 两表等值连接器演示：逐条演练各项语义并打印 OK/FAIL 判定。
// 不读命令行参数、不联网，退出码恒为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"ontology"
)

var passed, failed int

func check(name string, ok bool, detail ...any) {
	mark, tail := "OK  ", ""
	if ok {
		passed++
	} else {
		mark = "FAIL"
		failed++
		if len(detail) > 0 {
			tail = " " + fmt.Sprint(detail...)
		}
	}
	fmt.Printf("%s %s%s\n", mark, name, tail)
}

func shuffle(rows []map[string]any, seed int64) []map[string]any {
	out := make([]map[string]any, len(rows))
	copy(out, rows)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func main() {
	// 1. 两侧同为 nil 的键：Inner 不匹配，Left 保留左行。
	left := []map[string]any{{"id": nil, "v": "ln"}, {"id": 1, "v": "l1"}}
	right := []map[string]any{{"id": nil, "w": "rn"}, {"id": 1, "w": "r1"}}
	in, _, _ := ontology.Join(left, right, []string{"id"}, ontology.Inner)
	check("nil 键 Inner 不匹配(仅 id=1 成行)", len(in) == 1 && in[0]["v"] == "l1")
	lf, _, _ := ontology.Join(left, right, []string{"id"}, ontology.Left)
	check("nil 键左行 Left 下保留", len(lf) == 2)

	// 2. 两类未匹配计数分开。
	l2 := []map[string]any{{"id": nil}, {"k": 1}, {"id": 9}, {"id": 1}}
	r2 := []map[string]any{{"id": 1}}
	_, st, _ := ontology.Join(l2, r2, []string{"id"}, ontology.Left)
	check("空键未匹配计数=2(nil+缺失)", st.LeftNullKeyRows == 2, st.LeftNullKeyRows)
	check("有值无对应计数=1(id=9)", st.LeftUnmatchedRows == 1, st.LeftUnmatchedRows)

	// 3. 3x2 重复键展开 6 行。
	l3 := []map[string]any{{"id": 1, "v": "a"}, {"id": 1, "v": "b"}, {"id": 1, "v": "c"}}
	r3 := []map[string]any{{"id": 1, "w": "x"}, {"id": 1, "w": "y"}}
	out3, st3, _ := ontology.Join(l3, r3, []string{"id"}, ontology.Inner)
	check("3x2 展开=6 行", len(out3) == 6 && st3.MatchedPairs == 6 && st3.MaxKeyExpansion == 6)

	// 4. 左右表各自打乱后结果逐元素一致。
	base, _, _ := ontology.Join(l3, r3, []string{"id"}, ontology.Inner)
	same := true
	for seed := int64(1); seed <= 5 && same; seed++ {
		got, _, _ := ontology.Join(shuffle(l3, seed), shuffle(r3, seed+9),
			[]string{"id"}, ontology.Inner)
		same = reflect.DeepEqual(base, got)
	}
	check("打乱左右表后结果一致", same)

	// 5. 连接键类型冲突是可判定错误。
	_, _, err := ontology.Join(
		[]map[string]any{{"id": "1"}}, []map[string]any{{"id": int64(1)}},
		[]string{"id"}, ontology.Inner)
	var kte *ontology.KeyTypeError
	check("string vs int64 报 KeyTypeError",
		errors.As(err, &kte) && kte.Key == "id", err)

	// 6. 数值语义：int64==float64，NaN 不等，±0.0 相等。
	n, _, _ := ontology.Join(
		[]map[string]any{{"id": int64(42)}}, []map[string]any{{"id": 42.0}},
		[]string{"id"}, ontology.Inner)
	check("int64(42) 与 42.0 相等", len(n) == 1)
	nan := math.NaN()
	n, stN, _ := ontology.Join(
		[]map[string]any{{"id": nan}}, []map[string]any{{"id": nan}},
		[]string{"id"}, ontology.Inner)
	check("NaN 永不相等且计入空键", len(n) == 0 && stN.LeftNullKeyRows == 1)
	n, _, _ = ontology.Join(
		[]map[string]any{{"id": 0.0}}, []map[string]any{{"id": math.Copysign(0, -1)}},
		[]string{"id"}, ontology.Inner)
	check("+0.0 与 -0.0 相等", len(n) == 1)

	// 7. 同名非连接键属性：右表不覆盖左表。
	n, _, _ = ontology.Join(
		[]map[string]any{{"id": 1, "name": "L"}},
		[]map[string]any{{"id": 1, "name": "R"}}, []string{"id"}, ontology.Inner)
	rv, ok := ontology.RightValue(n[0], "name")
	check("同名属性不覆盖(L 保留,R 在 right.name)",
		n[0]["name"] == "L" && ok && rv == "R")

	fmt.Printf("== 总计 %d 项：%d OK, %d FAIL ==\n", passed+failed, passed, failed)
}
