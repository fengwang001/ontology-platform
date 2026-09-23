// demo 逐条演示并判定计划选择器的关键性质，每行以 OK/FAIL 开头。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/catalog"
	"ontology/plan"
	"ontology/stats"
)

var failures int

func check(name string, ok bool, detail string) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", mark, name, detail)
}

// ulpCatalog 构造数学等代价但浮点差 1 ULP 的场景（参数见 DESIGN.md）。
func ulpCatalog(order []string) *catalog.Catalog {
	rows := map[string]uint64{"A": 2945, "B": 3420, "C": 6660, "D": 3354}
	c := catalog.New()
	for _, n := range order {
		c.AddTable(n, rows[n])
	}
	cols := map[string]map[string]uint64{
		"A": {"x": 944}, "B": {"x": 944, "y": 820}, "C": {"y": 820, "z": 233}, "D": {"z": 233},
	}
	for n, cs := range cols {
		ts := &stats.TableStats{Table: n, Rows: rows[n], Columns: map[string]*stats.ColumnStats{}}
		for col, ndv := range cs {
			ts.Columns[col] = &stats.ColumnStats{Name: col, NDV: ndv}
		}
		c.SetStats(ts)
	}
	must(c.AddPredicate(catalog.ColRef{Table: "A", Column: "x"}, catalog.ColRef{Table: "B", Column: "x"}))
	must(c.AddPredicate(catalog.ColRef{Table: "B", Column: "y"}, catalog.ColRef{Table: "C", Column: "y"}))
	must(c.AddPredicate(catalog.ColRef{Table: "C", Column: "z"}, catalog.ColRef{Table: "D", Column: "z"}))
	return c
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func bestOf(c *catalog.Catalog) (*plan.Plan, error) {
	v, _, err := c.Analyze()
	if err != nil {
		return nil, err
	}
	res, err := plan.Select(v)
	if err != nil {
		return nil, err
	}
	return res.Best, nil
}

func chainCatalog(n int, disconnected bool) *catalog.Catalog {
	c := catalog.New()
	name := func(i int) string { return fmt.Sprintf("T%02d", i) }
	for i := 0; i < n; i++ {
		c.AddTable(name(i), uint64(1000*(i+1)))
	}
	for i := 0; i+1 < n; i++ {
		if disconnected && i == n/2-1 {
			continue // 断开成两段
		}
		must(c.AddPredicate(catalog.ColRef{Table: name(i), Column: "k"},
			catalog.ColRef{Table: name(i + 1), Column: "k"}))
	}
	return c
}

func main() {
	// 1. 等代价按名字序选中。
	best, err := bestOf(ulpCatalog([]string{"A", "B", "C", "D"}))
	check("等代价按名字序选中", err == nil && best.String() == "(((A⋈B)⋈C)⋈D)", best.String())
	// 2. 登记顺序打乱 20 次结果逐字节相同。
	order := []string{"A", "B", "C", "D"}
	same := true
	for i := 0; i < 20; i++ {
		order = []string{order[3], order[0], order[1], order[2]}
		if i%3 == 2 {
			order[1], order[2] = order[2], order[1]
		}
		b, err := bestOf(ulpCatalog(order))
		same = same && err == nil && b.String() == best.String()
	}
	check("登记顺序打乱20次结果一致", same, "plan="+best.String())
	// 3. 链式谓词不出现笛卡尔积。
	chain, err := bestOf(chainCatalog(4, false))
	check("链式谓词无笛卡尔积", err == nil && chain.CrossCount() == 0, chain.String())
	// 4. 不连通图恰好一次笛卡尔积且在最后一步。
	disc, err := bestOf(chainCatalog(4, true))
	check("不连通图恰好一次笛卡尔积且在最后",
		err == nil && disc.CrossCount() == 1 && disc.Cross, disc.String())
	// 5. n=12 的枚举复杂度与 n! 对比。
	v, _, _ := chainCatalog(12, false).Analyze()
	res, err := plan.Select(v)
	fact := uint64(1)
	for i := uint64(2); i <= 12; i++ {
		fact *= i
	}
	check("n=12 枚举远小于 n!", err == nil && res.Subsets() <= 1<<12 && res.Partitions() <= 531441 &&
		res.Partitions()*100 < fact,
		fmt.Sprintf("subsets=%d partitions=%d 12!=%d", res.Subsets(), res.Partitions(), fact))
	// 6-8. 三种统计异常各一例。
	_, miss, _ := chainCatalog(2, false).Analyze() // 无统计 → 缺失
	missing := false
	for _, w := range miss {
		missing = missing || errors.Is(w, catalog.ErrMissingStats)
	}
	check("缺失统计回退默认值并警告", missing, "errors.Is(ErrMissingStats)")
	staleCat := catalog.New()
	staleCat.AddTable("S", 1000)
	staleCat.SetStats(&stats.TableStats{Table: "S", Rows: 100})
	_, staleWarn, _ := staleCat.Analyze()
	stale := len(staleWarn) > 0 && errors.Is(staleWarn[0], catalog.ErrStaleStats)
	check("过期统计按目录行数校正", stale, "errors.Is(ErrStaleStats)")
	corruptCat := catalog.New()
	corruptCat.AddTable("K", 100)
	corruptCat.SetStats(&stats.TableStats{Table: "K", Rows: 100, Columns: map[string]*stats.ColumnStats{
		"x": {Name: "x", NDV: 10, Hist: &stats.Histogram{Bounds: []float64{0, 5, 5}, Counts: []uint64{50, 50}}},
	}})
	_, _, corruptErr := corruptCat.Analyze()
	check("损坏统计报错并指出表列",
		errors.Is(corruptErr, stats.ErrCorruptStats) &&
			strings.Contains(corruptErr.Error(), "K") && strings.Contains(corruptErr.Error(), "x"),
		fmt.Sprint(corruptErr))
	// 9. n=20 触发硬上限。
	v20, _, _ := chainCatalog(20, false).Analyze()
	_, err = plan.Select(v20)
	check("n=20 触发表数上限", errors.Is(err, plan.ErrTooManyTables), fmt.Sprint(err))

	total := 9
	fmt.Printf("TOTAL %d/%d OK\n", total-failures, total)
	if failures > 0 {
		os.Exit(1)
	}
}
