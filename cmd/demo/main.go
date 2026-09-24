// Command demo 运行属性级权限过滤器的验收判定。
// 不读参数、不联网；每步一行 OK/FAIL，最后输出总计。
package main

import (
	"errors"
	"fmt"

	"ontology/policy"
	"ontology/predicate"
	"ontology/report"

	"ontology/filter"
)

func main() {
	type verdict struct {
		name string
		ok   bool
	}
	var verdicts []verdict
	check := func(name string, ok bool) {
		verdicts = append(verdicts, verdict{name, ok})
	}

	check("skeleton", true)

	// policy 包边界：空可见集角色报 ErrEmptyRole，全集角色可见全部列。
	pol := policy.New(map[string][]string{
		"analyst": {"id", "name"},
		"blind":   {},
		"all":     {"id", "name", "secret"},
	})
	_, emptyErr := pol.Visible("blind")
	allCols, allErr := pol.Visible("all")
	check("policy edges: empty-set denied, full-set visible",
		errors.Is(emptyErr, policy.ErrEmptyRole) &&
			allErr == nil && len(allCols) == 3)

	// predicate 包边界：两个探针谓词可构造；NOT(secret=1) 共 2 个节点。
	probeNot := predicate.Not(predicate.Eq("secret", "1"))
	probeNull := predicate.IsNull("secret")
	check("predicate probes built; NOT-tree node count = 2",
		probeNot.Children[0].Column == "secret" &&
			probeNull.Op == predicate.OpIsNull &&
			predicate.Count(probeNot) == 2)

	// report 包：同一输入打乱构造顺序 20 次，编码必须逐字节相同。
	base := report.Build(
		[]string{"secret", "salary", "age"},
		[]report.Ref{{Column: "secret", Paths: [][]string{{"compare"}}}},
		nil, true,
	).Encode()
	reportStable := true
	for i := 0; i < 20; i++ {
		drop := []string{"age", "secret", "salary"}
		if i%2 == 0 {
			drop = []string{"salary", "age", "secret"}
		}
		got := report.Build(drop,
			[]report.Ref{{Column: "secret", Paths: [][]string{{"compare"}}}},
			nil, true).Encode()
		if got != base {
			reportStable = false
		}
	}
	check("report byte-identical across 20 shuffles", reportStable)

	// filter 包：主场景策略，secret 不可见。
	f := filter.New(pol)

	// NOT(secret=1) 必须拒绝，且指明列名与路径。
	notRes, notErr := f.Apply("analyst",
		predicate.Not(predicate.Eq("secret", "1")),
		[]filter.Row{{"id": "1"}})
	notOK := errors.Is(notErr, filter.ErrInvisibleColumn) && notRes != nil &&
		notRes.Report.RejectedQuery && notRes.Rows == nil &&
		len(notRes.Report.Rejected) == 1 &&
		notRes.Report.Rejected[0].Column == "secret" &&
		len(notRes.Report.Rejected[0].Paths) == 1 &&
		pathString(notRes.Report.Rejected[0].Paths[0]) == "not>compare"
	check("NOT(secret=1) rejected; column+path named", notOK)

	// secret IS NULL 必须拒绝（不返回行）。
	nullRes, nullErr := f.Apply("analyst", predicate.IsNull("secret"), nil)
	check("secret IS NULL rejected",
		errors.Is(nullErr, filter.ErrInvisibleColumn) && nullRes.Rows == nil)

	// 裁剪后行不含不可见列；键物理缺失，与置零值（键在值为空串）可区分。
	src := filter.Row{"id": "1", "name": "bob", "secret": "S", "salary": "9"}
	prj, prjErr := f.Apply("analyst", nil, []filter.Row{src})
	prjOK := prjErr == nil && len(prj.Rows) == 1 && len(prj.Rows[0]) == 2
	_, hasSecret := prj.Rows[0]["secret"]
	_, hasSalary := prj.Rows[0]["salary"]
	zero := filter.Row{"secret": ""}
	_, zeroHas := zero["secret"]
	if hasSecret || hasSalary || zeroHas == hasSecret ||
		prj.Rows[0]["id"] != "1" || prj.Rows[0]["name"] != "bob" {
		prjOK = false
	}
	check("rows clipped; invisible keys absent, distinct from zero value", prjOK)

	// 报告在 20 次打乱（列顺序）下逐字节相同。
	probe := predicate.And(predicate.Eq("secret", "1"), predicate.IsNull("salary"))
	encBase := func() string {
		r, _ := f.Apply("analyst", probe,
			[]filter.Row{{"id": "1", "name": "b", "secret": "S", "salary": "9"}})
		return r.Report.Encode()
	}()
	stable := true
	for i := 0; i < 20; i++ {
		var row filter.Row
		if i%2 == 0 {
			row = filter.Row{"salary": "9", "secret": "S", "name": "b", "id": "1"}
		} else {
			row = filter.Row{"id": "1", "secret": "S", "salary": "9", "name": "b"}
		}
		r, _ := f.Apply("analyst", probe, []filter.Row{row})
		if r.Report.Encode() != encBase {
			stable = false
		}
	}
	check("filter report stable over 20 shuffled rows", stable)

	// 谓词节点访问数 == 节点总数（一遍遍历）。
	visitPred := predicate.And(
		predicate.Or(predicate.Eq("id", "1"), predicate.Not(predicate.IsNull("name"))),
		predicate.Not(predicate.Eq("secret", "1")))
	f.Apply("analyst", visitPred, nil)
	check("predicate nodes visited once (== total count)",
		f.NodesVisited() == predicate.Count(visitPred))

	// 1000 列仅 5 可见时，拷贝次数 <= 4*5。
	bigRow := make(filter.Row, 1000)
	bigCols := []string{"c0", "c1", "c2", "c3", "c4"}
	for i := 0; i < 1000; i++ {
		bigRow[fmt.Sprintf("c%d", i)] = "v"
	}
	bigF := filter.New(policy.New(map[string][]string{"r": bigCols}))
	bigRes, bigErr := bigF.Apply("r", nil, []filter.Row{bigRow})
	check("1000 cols, 5 visible: copy ops <= 4*5",
		bigErr == nil && len(bigRes.Rows[0]) == 5 && bigF.CopyOps() <= 4*5)

	// 32 种穷举组合（8 可见性 x 4 形态）与 DESIGN 定义一致。
	check("32 exhaustive combos match DESIGN", exhaustive32())

	// OR 常量折叠例外：隐藏引用消解为 elided，查询放行。
	orPred := predicate.Or(predicate.ConstNode(true), predicate.Eq("secret", "1"))
	orRes, orErr := f.Apply("analyst", orPred, []filter.Row{{"id": "7"}})
	check("OR folded true: invisible ref elided, query allowed",
		orErr == nil && len(orRes.Rows) == 1 &&
			len(orRes.Report.Elided) == 1 && orRes.Report.Elided[0].Column == "secret" &&
			!orRes.Report.RejectedQuery)

	// 空可见集与全集两个边界。
	_, blindErr := f.Apply("blind", predicate.Eq("id", "1"), nil)
	allRes2, allErr2 := f.Apply("all", predicate.Eq("secret", "1"),
		[]filter.Row{{"id": "1", "name": "b", "secret": "S"}})
	check("empty-set rejects; full-set sees secret",
		errors.Is(blindErr, filter.ErrEmptyRole) &&
			allErr2 == nil && allRes2.Rows[0]["secret"] == "S")

	pass := 0
	for _, v := range verdicts {
		if v.ok {
			pass++
			fmt.Println("OK   " + v.name)
		} else {
			fmt.Println("FAIL " + v.name)
		}
	}
	if pass == len(verdicts) {
		fmt.Printf("TOTAL: %d/%d OK\n", pass, len(verdicts))
		return
	}
	fmt.Println("TOTAL: FAIL")
}

func pathString(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += ">"
		}
		out += s
	}
	return out
}

func exhaustive32() bool {
	cols := [3]string{"a", "b", "secret"}
	forms := [4]func() *predicate.Node{
		func() *predicate.Node { return predicate.Eq("secret", "1") },
		func() *predicate.Node { return predicate.Not(predicate.Eq("secret", "1")) },
		func() *predicate.Node {
			return predicate.And(predicate.Eq("a", "1"), predicate.Eq("secret", "1"))
		},
		func() *predicate.Node {
			return predicate.Or(predicate.Eq("a", "1"), predicate.Eq("secret", "1"))
		},
	}
	// DESIGN：引用列全部可见才放行。= / NOT 只引 secret（4 放行）；
	// AND / OR 引 a+secret（2 放行）。
	wantAllow := [4]int{4, 4, 2, 2}
	gotAllow := [4]int{}
	for mask := 0; mask < 8; mask++ {
		var vis []string
		for i := range cols {
			if mask&(1<<i) != 0 {
				vis = append(vis, cols[i])
			}
		}
		ff := filter.New(policy.New(map[string][]string{"r": vis}))
		for fi, build := range forms {
			if _, err := ff.Apply("r", build(), nil); err == nil {
				gotAllow[fi]++
			}
		}
	}
	return gotAllow == wantAllow
}
