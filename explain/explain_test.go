package explain

import (
	"strings"
	"testing"

	"ontology/catalog"
	"ontology/plan"
	"ontology/stats"
)

// build 登记两表（可选统计）并返回渲染结果。
func build(t *testing.T, mutate func(c *catalog.Catalog)) (string, []error) {
	t.Helper()
	c := catalog.New()
	c.AddTable("A", 1000)
	c.AddTable("B", 2000)
	if err := c.AddPredicate(catalog.ColRef{Table: "A", Column: "x"},
		catalog.ColRef{Table: "B", Column: "y"}); err != nil {
		t.Fatal(err)
	}
	mutate(c)
	v, warnings, err := c.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	res, err := plan.Select(v)
	if err != nil {
		t.Fatal(err)
	}
	return Render(res.Best, v, warnings), warnings
}

func colStats(table string, rows uint64, col string, ndv uint64) *stats.TableStats {
	return &stats.TableStats{Table: table, Rows: rows, Columns: map[string]*stats.ColumnStats{
		col: {Name: col, NDV: ndv},
	}}
}

func TestRenderAnnotations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(c *catalog.Catalog)
		want    []string
		notWant []string
	}{
		{"healthy plan has steps and totals", func(c *catalog.Catalog) {
			c.SetStats(colStats("A", 1000, "x", 500))
			c.SetStats(colStats("B", 2000, "y", 800))
		}, []string{"1. Scan A card=1000 cost=1000", "3. Join(A,B) card=2500 cost=6000",
			"pred=A.x=B.y sel=0.00125", "== 总代价 6000"},
			[]string{"[估计不可靠", "[统计过期", "[笛卡尔积]"}},
		{"missing stats annotated unreliable", func(c *catalog.Catalog) {
			c.SetStats(colStats("A", 1000, "x", 500))
		}, []string{"[估计不可靠: 缺列统计, 已回退默认选择率]", "sel=0.1"},
			nil},
		{"stale stats annotated and corrected", func(c *catalog.Catalog) {
			c.SetStats(colStats("A", 100, "x", 500)) // 与目录 1000 偏差 >20%
			c.SetStats(colStats("B", 2000, "y", 800))
		}, []string{"1. Scan A card=1000 cost=1000 [统计过期: 已按目录行数校正]"},
			nil},
	}
	for _, tc := range cases {
		out, _ := build(t, tc.mutate)
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: output missing %q\n%s", tc.name, w, out)
			}
		}
		for _, nw := range tc.notWant {
			if strings.Contains(out, nw) {
				t.Errorf("%s: output should not contain %q\n%s", tc.name, nw, out)
			}
		}
	}
}

func TestRenderMultiPredAndCross(t *testing.T) {
	c := catalog.New()
	c.AddTable("A", 100)
	c.AddTable("B", 100)
	c.AddTable("C", 100)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.AddPredicate(catalog.ColRef{Table: "A", Column: "x"}, catalog.ColRef{Table: "B", Column: "x"}))
	must(c.AddPredicate(catalog.ColRef{Table: "A", Column: "y"}, catalog.ColRef{Table: "B", Column: "y"}))
	// C 与 {A,B} 之间无谓词 → 顶层必为笛卡尔积。
	c.SetStats(colStats("A", 100, "x", 50))
	c.SetStats(colStats("B", 100, "x", 50))
	v, _, err := c.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	res, err := plan.Select(v)
	if err != nil {
		t.Fatal(err)
	}
	out := Render(res.Best, v, nil)
	for _, w := range []string{"[独立性假设: 2个谓词选择率相乘]", "[笛卡尔积]"} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n%s", w, out)
		}
	}
}
