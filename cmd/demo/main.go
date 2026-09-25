package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/col"
	"ontology/prune"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func main() {
	s5, _ := col.NewSchema([]string{"a", "b", "c", "d", "e"})
	eng := prune.NewEngine(s5)
	outs := []prune.Output{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	}
	// 六步之 1-3：每定义一个输出列后的保留集
	stepKept := [][]string{{"a"}, {"a", "b", "c"}, {"a", "b", "c", "d"}}
	keptOK := true
	for i := range outs {
		if eng.SetProjection(outs[:i+1]) != nil {
			keptOK = false
		}
		if fmt.Sprint(eng.KeptCols()) != fmt.Sprint(stepKept[i]) {
			keptOK = false
		}
	}
	check("六步1-3: 保留集 {a}/{a,b,c}/{a,b,c,d}，裁剪集含 e", keptOK)

	// 六步之 4-6：三行的投影输出（别名取值 + 聚合逐行现算）
	rows := []map[string]int{
		{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5},
		{"a": 6, "b": 7, "c": 8, "d": 9, "e": 10},
		{"a": 11, "b": 12, "c": 13, "d": 14, "e": 15},
	}
	for _, r := range rows {
		if eng.Apply(r) != nil {
			check("六步4-6: apply", false)
		}
	}
	check("六步4-6: 三行输出 [1 5 4]/[6 15 9]/[11 25 14]",
		fmt.Sprint(eng.View()) == "[[1 5 4] [6 15 9] [11 25 14]]")

	// 四类可判定错误互不相同 + 被拒后状态不变
	st, _ := api.New([]string{"a", "b", "c", "d", "e"})
	_ = st.SetProjection(outs)
	_ = st.Apply(rows[0])
	before := fmt.Sprint(st.View())
	e1 := func() error { _, err := api.New([]string{"a", "a"}); return err }()
	e2 := st.SetProjection([]api.Output{{Out: "z", Refs: []string{"nope"}}})
	e3 := st.SetProjection([]api.Output{{Out: "x", Refs: []string{"a"}}, {Out: "x", Refs: []string{"b"}}})
	e4 := st.Apply(map[string]int{"a": 1})
	e5 := st.Apply(map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "zz": 6})
	errs := []error{e1, e2, e3, e4, e5}
	distinct := true
	for i := range errs {
		if errs[i] == nil {
			distinct = false
		}
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	check("四类错误可判定且互不相同", distinct)
	check("被拒后状态不变且可继续用",
		fmt.Sprint(st.View()) == before && st.Apply(rows[1]) == nil &&
			fmt.Sprint(st.OutputNames()) == "[x sum y]")

	// 宽表：投影只引 2 列，输出与被裁列无关（读取计数见 prune 内部测试）
	for _, m := range []int{100, 1000, 10000} {
		cols := make([]string, m)
		row := map[string]int{}
		for i := range cols {
			cols[i] = fmt.Sprintf("c%d", i)
			row[cols[i]] = i
		}
		w, err := api.New(cols)
		if err != nil || w.SetProjection([]api.Output{
			{Out: "p", Refs: []string{"c1"}}, {Out: "q", Refs: []string{"c2", "c3"}},
		}) != nil || w.Apply(row) != nil ||
			fmt.Sprint(w.View()) != "[[1 5]]" {
			check(fmt.Sprintf("宽表 m=%d", m), false)
		}
	}
	check("宽表 m=100..10000: 只读被引列，输出正确", true)

	// 并发只读视图一致
	var wg sync.WaitGroup
	base := fmt.Sprint(eng.View())
	same := true
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				if fmt.Sprint(eng.View()) != base {
					mu.Lock()
					same = false
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	check("并发只读视图逐行逐列一致", same)
	check("SelfCheck 四条不变量", st.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
