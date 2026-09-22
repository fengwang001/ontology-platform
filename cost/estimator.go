package cost

import (
	"ontology/catalog"
	"ontology/stats"
)

// Counters 统计估计过程的底层访问，用于证明估计只读统计、不扫行数据。
type Counters struct {
	RowDataAccesses int // 访问行数据次数：恒为 0
	StatsAccesses   int // 读取列统计次数
}

type edge struct {
	pred *catalog.Predicate
	c1   *stats.Column
	c2   *stats.Column
}

// Estimator 依据目录与谓词估计基数。内部只持有只读统计与谓词索引。
type Estimator struct {
	cat          *catalog.Catalog
	edges        map[string][]edge // 有序表名对 "a\x00b" -> 谓词
	predWarnings map[*catalog.Predicate][]catalog.Warning
	Counters     Counters
}

// NewEstimator 预解析全部谓词两侧的列统计（此时不访问行数据）。
func NewEstimator(c *catalog.Catalog) *Estimator {
	e := &Estimator{
		cat:          c,
		edges:        map[string][]edge{},
		predWarnings: map[*catalog.Predicate][]catalog.Warning{},
	}
	for _, p := range c.Predicates() {
		c1, c2, warns := c.Resolve(p)
		key := pairKey(p.LeftTable, p.RightTable)
		e.edges[key] = append(e.edges[key], edge{pred: p, c1: c1, c2: c2})
		e.predWarnings[p] = warns
		e.Counters.StatsAccesses += 2
	}
	return e
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

// LeafCardinality 返回叶子表当前基数：只用目录行数，不扫描行数据。
func (e *Estimator) LeafCardinality(t *catalog.Table) float64 {
	return float64(t.Rows)
}

// Cross 估计跨越左右两个已选表集合的连接信息：
// sels 为每条跨谓词的选择率，warns 聚合相关统计警告，predCount 为跨谓词数。
// predCount==0 表示两侧之间只能做笛卡尔积。
func (e *Estimator) Cross(lNames, rNames []string) (sels []float64, warns []catalog.Warning, predCount int) {
	lset := map[string]bool{}
	for _, n := range lNames {
		lset[n] = true
	}
	rset := map[string]bool{}
	for _, n := range rNames {
		rset[n] = true
	}
	for ln := range lset {
		for rn := range rset {
			for _, eg := range e.edges[pairKey(ln, rn)] {
				sel, _ := stats.Selectivity(eg.c1, eg.c2)
				sels = append(sels, sel)
				warns = append(warns, e.predWarnings[eg.pred]...)
				predCount++
			}
		}
	}
	return sels, warns, predCount
}
