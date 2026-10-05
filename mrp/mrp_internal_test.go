package mrp

import (
	"fmt"
	"testing"

	"ontology/bom"
	"ontology/stock"
)

// 菱形链：每层一个物料经两个中间件汇入下一层共用件，路径数 2^k。
// visitedItems/visitedEdges 必须与路径条数无关，恒等于物料数/关系数。
func TestDiamondChainLinearVisits(t *testing.T) {
	for _, k := range []int{5, 20} {
		st := stock.New()
		bg := bom.New()
		e, err := New(1, st, bg)
		if err != nil {
			t.Fatal(err)
		}
		addItem := func(name string) {
			if err := e.AddItem(name, 0, 0, 0, 1, 1); err != nil {
				t.Fatal(err)
			}
		}
		addComp := func(p, c string) {
			if err := e.AddComponent(p, c, 1, 0); err != nil {
				t.Fatal(err)
			}
		}
		itemCount := 0
		edgeCount := 0
		level := func(i int) string { return fmt.Sprintf("L%d", i) }
		for i := 0; i <= k; i++ {
			addItem(level(i))
			itemCount++
		}
		for i := 0; i < k; i++ {
			for _, s := range []string{"a", "b"} {
				m := fmt.Sprintf("M%d%s", i, s)
				addItem(m)
				itemCount++
				addComp(level(i), m)
				addComp(m, level(i+1))
				edgeCount += 2
			}
		}
		if err := e.Demand(level(0), 1, 1); err != nil {
			t.Fatal(err)
		}
		res, err := e.Run()
		if err != nil {
			t.Fatalf("k=%d Run: %v", k, err)
		}
		if e.visitedItems != itemCount {
			t.Errorf("k=%d: visitedItems=%d want %d", k, e.visitedItems, itemCount)
		}
		if e.visitedEdges != edgeCount {
			t.Errorf("k=%d: visitedEdges=%d want %d", k, e.visitedEdges, edgeCount)
		}
		// 最底层物料的毛需求 = 路径条数 2^k。
		if want := int64(1) << k; res.G[level(k)][1] != want {
			t.Errorf("k=%d: G(%s,1)=%d want %d", k, level(k), res.G[level(k)][1], want)
		}
		t.Logf("k=%d: 路径数 2^%d=%d, visitedItems=%d(物料数), visitedEdges=%d(关系数)",
			k, k, int64(1)<<k, e.visitedItems, e.visitedEdges)
	}
}
