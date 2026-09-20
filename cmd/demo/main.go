// demo 逐项演练多键排序器的核心语义，每步打印一行 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"

	"ontology"
)

var failed int

func check(name string, ok bool, detail string) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed++
	}
	fmt.Printf("%s %s: %s\n", mark, name, detail)
}

func equalRows(n int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"k": int64(7), "tag": i}
	}
	return rows
}

func tags(res *ontology.Result) []int {
	out := make([]int, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = r.Data["tag"].(int)
	}
	return out
}

func main() {
	// 1. 全键相等：输出下标严格递增。
	res, err := ontology.NewSorter(ontology.SortKey{Field: "k"}).Sort(equalRows(8))
	check("stable-all-equal", err == nil && sort.IntsAreSorted(res.Indices()),
		fmt.Sprintf("indices=%v", res.Indices()))

	// 2. 升序与降序下，相等行相对顺序一致。
	asc, _ := ontology.NewSorter(ontology.SortKey{Field: "k"}).Sort(equalRows(8))
	desc, _ := ontology.NewSorter(ontology.SortKey{Field: "k", Desc: true}).Sort(equalRows(8))
	check("desc-keeps-equal-order", reflect.DeepEqual(tags(asc), tags(desc)),
		fmt.Sprintf("asc=%v desc=%v", tags(asc), tags(desc)))

	// 3. 四种 升降序×空值位置 组合。
	nrows := []map[string]any{{"k": int64(2)}, {"k": nil}, {"k": int64(1)}}
	combos := []struct {
		name string
		key  ontology.SortKey
		want []int
	}{
		{"asc+nulls-first", ontology.SortKey{Field: "k", NullsFirst: true}, []int{1, 2, 0}},
		{"asc+nulls-last", ontology.SortKey{Field: "k"}, []int{2, 0, 1}},
		{"desc+nulls-first", ontology.SortKey{Field: "k", Desc: true, NullsFirst: true}, []int{1, 0, 2}},
		{"desc+nulls-last", ontology.SortKey{Field: "k", Desc: true}, []int{0, 2, 1}},
	}
	for _, c := range combos {
		r, err := ontology.NewSorter(c.key).Sort(nrows)
		check(c.name, err == nil && reflect.DeepEqual(r.Indices(), c.want),
			fmt.Sprintf("indices=%v", r.Indices()))
	}

	// 4. 缺失与 nil 位置相同，但可区分。
	mn := []map[string]any{{"k": nil}, {"k": int64(5)}, {"other": 1}}
	r, _ := ontology.NewSorter(ontology.SortKey{Field: "k", NullsFirst: true}).Sort(mn)
	kindOK := ontology.Classify(mn[0], "k") == ontology.NullNil &&
		ontology.Classify(mn[2], "k") == ontology.NullMissing
	check("missing-vs-nil", reflect.DeepEqual(r.Indices(), []int{0, 2, 1}) && kindOK,
		fmt.Sprintf("indices=%v nil-row=%v missing-row=%v",
			r.Indices(), ontology.Classify(mn[0], "k"), ontology.Classify(mn[2], "k")))

	// 5. 跨类型：可判定错误，带键名与两种类型。
	_, err = ontology.NewSorter(ontology.SortKey{Field: "k"}).Sort(
		[]map[string]any{{"k": "apple"}, {"k": int64(3)}})
	var tm *ontology.TypeMismatchError
	check("type-mismatch-error", errors.As(err, &tm) && tm.Key == "k", fmt.Sprintf("err=%v", err))

	// 6. NaN 走空值规则并计数。
	nanRows := []map[string]any{{"k": math.NaN()}, {"k": 1.5}}
	r, _ = ontology.NewSorter(ontology.SortKey{Field: "k", NullsFirst: true}).Sort(nanRows)
	check("nan-as-null", reflect.DeepEqual(r.Indices(), []int{0, 1}) && r.Stats.NaNValues > 0,
		fmt.Sprintf("indices=%v nan-count=%d", r.Indices(), r.Stats.NaNValues))

	// 7. 第一键分胜负后不再比较后续键。
	distinct := []map[string]any{
		{"a": int64(5), "b": "x"}, {"a": int64(1), "b": "y"}, {"a": int64(9), "b": "z"},
	}
	one, _ := ontology.NewSorter(ontology.SortKey{Field: "a"}).Sort(distinct)
	two, _ := ontology.NewSorter(
		ontology.SortKey{Field: "a"}, ontology.SortKey{Field: "b"}).Sort(distinct)
	check("short-circuit", one.Stats.Comparisons == two.Stats.Comparisons,
		fmt.Sprintf("1-key=%d 2-keys=%d", one.Stats.Comparisons, two.Stats.Comparisons))

	// 8. 输入切片与行不被修改。
	in := []map[string]any{{"k": int64(2)}, {"k": int64(1)}}
	_, _ = ontology.NewSorter(ontology.SortKey{Field: "k"}).Sort(in)
	check("input-untouched", in[0]["k"] == int64(2) && in[1]["k"] == int64(1),
		fmt.Sprintf("input=%v", in))

	fmt.Printf("SUMMARY: %d checks, %d failed\n", 12, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
