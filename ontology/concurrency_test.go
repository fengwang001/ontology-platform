package ontology

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// 长时间遍历与持续并发写入（含迁移与基数调整）交织时，
// 遍历结果必须与 AsOf 时刻切下的快照一致，且多次遍历互不干扰。
func TestLongTraversalWithConcurrentWrites(t *testing.T) {
	d := newDual()
	if _, err := d.defineObjectType("T", []PropertyDef{{ID: "p", Name: "n", Type: TypeInt}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.defineLinkType("L", "T", "T", ManyToMany); err != nil {
		t.Fatal(err)
	}

	// 构造网格图：120 个对象，每个对象链接到后两个对象。
	const n = 120
	for i := 0; i < n; i++ {
		if _, err := d.putObject("T", fmt.Sprintf("o%03d", i), map[string]Value{"n": int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		for _, j := range []int{i + 1, i + 2} {
			if j < n {
				if _, err := d.addLink("L", fmt.Sprintf("o%03d", i), fmt.Sprintf("o%03d", j)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	asOf := d.s.CurrentVersion()

	// 让遍历足够慢，与写入充分交织。
	stepHook = func() { time.Sleep(300 * time.Microsecond) }
	defer func() { stepHook = nil }()

	// 后台持续写入：新对象、新链接、属性写、迁移、基数调整。
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			id := fmt.Sprintf("w%04d", i)
			if _, err := d.s.PutObject("T", id, map[string]Value{"n": int64(i)}); err != nil {
				continue
			}
			d.s.AddLink("L", "o000", id)
			d.s.SetProperty("o001", "n", int64(1000+i))
			if i%10 == 0 {
				d.s.AdjustCardinality("L", OneToMany)
				d.s.AdjustCardinality("L", ManyToMany)
			}
			if i%15 == 0 {
				d.s.MigrateObjectType("T",
					[]PropertyDef{{ID: "p", Name: "n", Type: TypeInt},
						{ID: fmt.Sprintf("extra%d", i), Name: fmt.Sprintf("extra%d", i), Type: TypeString}},
					nil)
			}
		}
	}()

	req := TraverseRequest{Start: "o000", AsOf: asOf, MaxDepth: n + 1, MaxVisited: n + 1}

	// 并发发起 4 次同一历史时刻的遍历。
	results := make([]*TraverseResult, 4)
	var twg sync.WaitGroup
	for k := range results {
		twg.Add(1)
		go func(k int) {
			defer twg.Done()
			res, err := d.s.Traverse(req)
			if err != nil {
				t.Errorf("traversal %d: %v", k, err)
				return
			}
			results[k] = res
		}(k)
	}
	twg.Wait()
	close(stop)
	wg.Wait()

	// 与朴素模型在 AsOf 时刻的独立展开逐条对照。
	want, err := d.n.Traverse(req)
	if err != nil {
		t.Fatalf("naive: %v", err)
	}
	wn, we := normalize(want)
	for k, res := range results {
		if res == nil {
			t.Fatalf("traversal %d returned nil", k)
		}
		gn, ge := normalize(res)
		if !reflect.DeepEqual(gn, wn) {
			t.Fatalf("traversal %d nodes diverge from naive model", k)
		}
		if !reflect.DeepEqual(ge, we) {
			t.Fatalf("traversal %d edges diverge from naive model", k)
		}
		if len(res.Nodes) != n {
			t.Fatalf("traversal %d saw %d nodes, want %d (snapshot must not include later writes)", k, len(res.Nodes), n)
		}
	}
	// 多次遍历互不干扰：结果完全一致。
	for k := 1; k < len(results); k++ {
		if !reflect.DeepEqual(results[0], results[k]) {
			t.Fatalf("traversal 0 and %d differ", k)
		}
	}
}
