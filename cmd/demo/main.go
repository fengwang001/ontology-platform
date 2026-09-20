// Command demo exercises the multi-key sorter end to end and prints
// one OK/FAIL verdict per property, plus a final summary line.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"

	"ontology"
)

var checks, failures int

func check(name string, ok bool, detail string) {
	checks++
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

func increasing(indices []int) bool {
	for i := 1; i < len(indices); i++ {
		if indices[i] <= indices[i-1] {
			return false
		}
	}
	return true
}

func main() {
	eq := make([]map[string]any, 8)
	for i := range eq {
		eq[i] = map[string]any{"k": "same", "id": i}
	}
	stable, _ := ontology.New(ontology.SortKey{Field: "k"}).Sort(eq)
	check("全键相等时输出下标递增", increasing(stable.Indices),
		fmt.Sprintf("indices=%v", stable.Indices))

	asc, _ := ontology.New(ontology.SortKey{Field: "k"}).Sort(eq)
	desc, _ := ontology.New(ontology.SortKey{Field: "k", Desc: true}).Sort(eq)
	check("降序不翻转相等行相对顺序", reflect.DeepEqual(asc.Indices, desc.Indices),
		fmt.Sprintf("asc=%v desc=%v", asc.Indices, desc.Indices))

	mixed := []map[string]any{{"v": int64(2)}, {"v": nil}, {"v": int64(1)}}
	combos := []struct {
		name string
		key  ontology.SortKey
		want []int
	}{
		{"升序+空值在前", ontology.SortKey{Field: "v", NullsFirst: true}, []int{1, 2, 0}},
		{"升序+空值在后", ontology.SortKey{Field: "v"}, []int{2, 0, 1}},
		{"降序+空值在前", ontology.SortKey{Field: "v", Desc: true, NullsFirst: true}, []int{1, 0, 2}},
		{"降序+空值在后", ontology.SortKey{Field: "v", Desc: true}, []int{0, 2, 1}},
	}
	for _, c := range combos {
		res, _ := ontology.New(c.key).Sort(mixed)
		check(c.name, reflect.DeepEqual(res.Indices, c.want),
			fmt.Sprintf("indices=%v want=%v", res.Indices, c.want))
	}

	nulls := []map[string]any{{"v": int64(1)}, {"v": nil}, {"w": 0}}
	res, _ := ontology.New(ontology.SortKey{Field: "v", NullsFirst: true}).Sort(nulls)
	sameSpot := reflect.DeepEqual(res.Indices, []int{1, 2, 0})
	distinguishable := ontology.NullKindOf(nulls[1], "v") == ontology.NilValue &&
		ontology.NullKindOf(nulls[2], "v") == ontology.Missing
	check("缺失与nil位置相同且可区分", sameSpot && distinguishable,
		fmt.Sprintf("indices=%v nil=%v missing=%v",
			res.Indices, ontology.NullKindOf(nulls[1], "v"), ontology.NullKindOf(nulls[2], "v")))

	_, err := ontology.New(ontology.SortKey{Field: "v"}).Sort(
		[]map[string]any{{"v": "str"}, {"v": int64(7)}})
	var inc *ontology.IncomparableError
	check("跨类型返回可判定错误", errors.As(err, &inc) && inc.Key == "v", fmt.Sprintf("err=%v", err))

	nanRows := []map[string]any{{"v": math.NaN()}, {"v": 2.5}, {"v": 1.5}}
	nanRes, _ := ontology.New(ontology.SortKey{Field: "v", NullsFirst: true}).Sort(nanRows)
	check("NaN走空值规则并计数",
		reflect.DeepEqual(nanRes.Indices, []int{0, 2, 1}) && nanRes.NaNs == 1,
		fmt.Sprintf("indices=%v nans=%d", nanRes.Indices, nanRes.NaNs))

	distinct := []map[string]any{
		{"a": int64(3), "b": "x"}, {"a": int64(1), "b": "y"}, {"a": int64(2), "b": "z"},
	}
	one, _ := ontology.New(ontology.SortKey{Field: "a"}).Sort(distinct)
	three, _ := ontology.New(
		ontology.SortKey{Field: "a"},
		ontology.SortKey{Field: "b"},
		ontology.SortKey{Field: "b", Desc: true},
	).Sort(distinct)
	check("首键分胜负后不再比较后续键", one.Comparisons == three.Comparisons,
		fmt.Sprintf("1键=%d次 3键=%d次", one.Comparisons, three.Comparisons))

	orig := []map[string]any{{"v": int64(2)}, {"v": int64(1)}}
	if _, err := ontology.New(ontology.SortKey{Field: "v"}).Sort(orig); err != nil {
		check("输入未被修改", false, err.Error())
	} else {
		untouched := orig[0]["v"] == int64(2) && orig[1]["v"] == int64(1) && len(orig) == 2
		check("输入未被修改", untouched, fmt.Sprintf("rows=%v", orig))
	}

	if failures == 0 {
		fmt.Printf("OK 总计: %d/%d 项通过\n", checks, checks)
		return
	}
	fmt.Printf("FAIL 总计: %d/%d 项通过\n", checks-failures, checks)
	os.Exit(1)
}
