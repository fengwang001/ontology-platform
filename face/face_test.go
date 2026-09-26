package face

import (
	"testing"

	"ontology/pg"
)

// TestPrevChecksConstant 中心节点连 m 个叶子（m 多档），反复对中心做
// next 定位，断言每次检查的邻接节点数 ≤2、不随 m 增长，证明 O(1) 索引定位。
// checks 是非导出字段，只能在本包内测试中直接读取，不经任何导出接口。
func TestPrevChecksConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		g, err := pg.New(m + 1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= m; i++ {
			if err := g.AddEdge(0, i); err != nil {
				t.Fatal(err)
			}
		}
		tr := &Tracer{}
		const reps = 50
		base := tr.checks
		for i := 0; i < reps; i++ {
			if _, _, ok := tr.next(g, 1+i%m, 0); !ok {
				t.Fatalf("m=%d: next 定位失败", m)
			}
		}
		if got := (tr.checks - base) / reps; got > 2 {
			t.Fatalf("m=%d: 每次定位检查 %d 个邻接节点，应 ≤2 且不随 m 增长", m, got)
		}
	}
}

// TestTraceNaiveAgree 星图多档规模下 Trace 与朴素参照结果一致。
func TestTraceNaiveAgree(t *testing.T) {
	for _, m := range []int{2, 3, 100} {
		g, _ := pg.New(m + 1)
		for i := 1; i <= m; i++ {
			if err := g.AddEdge(0, i); err != nil {
				t.Fatal(err)
			}
		}
		if got, want := len(Trace(g)), len(NaiveFaces(g)); got != want {
			t.Fatalf("m=%d: Trace=%d 朴素=%d", m, got, want)
		}
		if c := Components(g); c != 1 {
			t.Fatalf("m=%d: 分量数=%d 应为 1", m, c)
		}
	}
}
