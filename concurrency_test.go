package ontology_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology"
	"ontology/naive"
)

// TestConcurrentQueriesAndMutations 在并发增删链接、切换隔离状态与并发
// 查询下运行（配合 -race 使用），并在所有操作结束后验证最终图状态上
// 主引擎与朴素穷举模型的结果一致。
func TestConcurrentQueriesAndMutations(t *testing.T) {
	g, err := ontology.NewGraph([]ontology.ObjectTypeSpec{{ID: "T1"}}, []ontology.Category{"cA", "cB"})
	must(t, err)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Bidirectional: true, Category: "cA", Cost: 1},
		ontology.LinkTypeSpec{ID: "LB", From: "T1", To: "T1", Category: "cB", Cost: 2},
	)
	const numObjects = 20
	for i := 0; i < numObjects; i++ {
		must(t, g.AddObject(ontology.ObjectID(fmt.Sprintf("o%d", i)), "T1"))
	}
	obj := func(i int) ontology.ObjectID { return ontology.ObjectID(fmt.Sprintf("o%d", i)) }
	// 预先建立一条环，保证大多数查询可达。
	for i := 0; i < numObjects; i++ {
		must(t, g.AddLink(ontology.LinkID(fmt.Sprintf("ring-%d", i)), "LA", obj(i), obj((i+1)%numObjects)))
	}

	// 并发冒烟测试使用定长模式，将单次查询的开销限制在有界范围内；
	// 星号标记的语义由专门的单元测试与差分测试覆盖。
	pattern := []ontology.PatternElem{{Any: true}, {Any: true}}
	var wg sync.WaitGroup

	// 写操作：增删链接、切换隔离状态。
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for j := 0; j < 50; j++ {
				id := ontology.LinkID(fmt.Sprintf("w%d-l%d", w, j))
				from, to := obj(rng.Intn(numObjects)), obj(rng.Intn(numObjects))
				if err := g.AddLink(id, "LB", from, to); err != nil {
					t.Errorf("AddLink: %v", err)
				}
				if err := g.SetIsolation(obj(rng.Intn(numObjects)), j%2 == 0); err != nil {
					t.Errorf("SetIsolation: %v", err)
				}
				if j%3 == 0 {
					if err := g.RemoveLink(id); err != nil {
						t.Errorf("RemoveLink: %v", err)
					}
				}
			}
		}(w)
	}

	// 读操作：并发查询。每次查询都必须看到完整快照并返回确定的结果。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1000 + r)))
			for j := 0; j < 100; j++ {
				q := ontology.PathQuery{
					Start:     obj(rng.Intn(numObjects)),
					End:       obj(rng.Intn(numObjects)),
					Pattern:   pattern,
					Principal: "alice",
				}
				res, err := g.Query(q)
				if err != nil {
					t.Errorf("Query: %v", err)
					continue
				}
				if res.Found && len(res.Objects) != len(res.Links)+1 {
					t.Errorf("inconsistent path: %+v", res)
				}
			}
		}(r)
	}
	wg.Wait()

	// 并发结束后，在最终快照上对照主引擎与朴素模型。
	// 对照使用定长模式，将朴素枚举的规模控制在多项式内。
	snap := g.Snapshot()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		q := ontology.PathQuery{
			Start:     obj(rng.Intn(numObjects)),
			End:       obj(rng.Intn(numObjects)),
			Pattern:   []ontology.PatternElem{{Any: true}, {Any: true}},
			Principal: "alice",
		}
		got, err1 := g.Query(q)
		want, err2 := naive.Solve(snap, q)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("query %+v: engine err=%v, naive err=%v", q, err1, err2)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("query %+v: engine=%+v, naive=%+v", q, got, want)
		}
	}
}
