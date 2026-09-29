package lineage

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentDerivationsComplete 并发派生下血缘必须完整，且与并发查询共存。
func TestConcurrentDerivationsComplete(t *testing.T) {
	tr := NewTrackerWithLogger(nil)
	ctx := context.Background()
	mustRegister(t, tr, rref("root", 1))

	const n = 200

	// 与写入并发执行的查询：只要求不 panic / 无数据竞争。
	readerDone := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = tr.Downstream(ctx, rref("root", 1))
			}
		}
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("o%03d", i)
			errCh <- tr.RecordDerivation(ctx, Record{
				Operation: "fanout",
				Inputs:    []ObjectRef{rref("root", 1)},
				Outputs:   []ObjectRef{rref(id, 1)},
			})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent fanout derivation failed: %v", err)
		}
	}
	close(stop)
	<-readerDone

	g, err := tr.Downstream(ctx, rref("root", 1))
	if err != nil {
		t.Fatalf("Downstream(root): %v", err)
	}
	if len(g.Edges) != n {
		t.Fatalf("fanout edges = %d, want %d (lost lineage under concurrency)", len(g.Edges), n)
	}

	// 第二阶段并发：每个 oi 派生 mi，输入版本互不相交，全部必须成功且血缘完整。
	var wg2 sync.WaitGroup
	errCh2 := make(chan error, n)
	for i := 0; i < n; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			oi := fmt.Sprintf("o%03d", i)
			mi := fmt.Sprintf("m%03d", i)
			errCh2 <- tr.RecordDerivation(ctx, Record{
				Operation: "map",
				Inputs:    []ObjectRef{rref(oi, 1)},
				Outputs:   []ObjectRef{rref(mi, 1)},
			})
		}(i)
	}
	wg2.Wait()
	close(errCh2)
	for err := range errCh2 {
		if err != nil {
			t.Fatalf("concurrent map derivation failed: %v", err)
		}
	}

	gRoot, err := tr.Downstream(ctx, rref("root", 1))
	if err != nil {
		t.Fatalf("Downstream(root) after map: %v", err)
	}
	if len(gRoot.Edges) != 2*n {
		t.Fatalf("total edges = %d, want %d", len(gRoot.Edges), 2*n)
	}
	for i := 0; i < n; i++ {
		mi := fmt.Sprintf("m%03d", i)
		up, err := tr.Upstream(ctx, rref(mi, 1))
		if err != nil {
			t.Fatalf("Upstream(%s): %v", mi, err)
		}
		if len(up.Edges) != 2 {
			t.Fatalf("Upstream(%s) edges = %d, want 2 (root->o->m)", mi, len(up.Edges))
		}
	}
}

// TestOrderIndependentLineage 同一组互不依赖的派生以任意顺序提交，血缘图必须完全相同。
// 这些派生也是并发场景下可能交错的操作；交错顺序不得影响最终血缘。
func TestOrderIndependentLineage(t *testing.T) {
	const fanout = 30

	type op struct {
		name string
		in   []ObjectRef
		out  []ObjectRef
	}

	// 第一层：3 个来源 -> 各自对象；第二层：所有第一层对象做交叉合并。
	// 第一层各操作互不依赖；第二层依赖第一层，所以层内乱序、层间有序。
	var layer1 []op
	for i := 0; i < fanout; i++ {
		src := fmt.Sprintf("src%02d", i)
		obj := fmt.Sprintf("obj%02d", i)
		layer1 = append(layer1, op{name: "extract", in: []ObjectRef{rref(src, 1)}, out: []ObjectRef{rref(obj, 1)}})
	}

	build := func(order []int) map[string]string {
		tr := NewTrackerWithLogger(nil)
		ctx := context.Background()
		for i := 0; i < fanout; i++ {
			mustRegister(t, tr, rref(fmt.Sprintf("src%02d", i), 1))
		}
		for _, idx := range order {
			o := layer1[idx]
			mustRecord(t, tr, o.name, o.in, o.out)
		}
		// 交叉合并：相邻对象两两 merge 出新对象。
		var merges []op
		for i := 0; i+1 < fanout; i++ {
			left := fmt.Sprintf("obj%02d", i)
			right := fmt.Sprintf("obj%02d", i+1)
			merged := fmt.Sprintf("merged%02d", i)
			merges = append(merges, op{
				name: "merge",
				in:   []ObjectRef{rref(left, 1), rref(right, 1)},
				out:  []ObjectRef{rref(merged, 1)},
			})
		}
		mergeOrder := make([]int, len(merges))
		for i := range mergeOrder {
			mergeOrder[i] = i
		}
		rng := rand.New(rand.NewSource(7))
		rng.Shuffle(len(mergeOrder), func(i, j int) { mergeOrder[i], mergeOrder[j] = mergeOrder[j], mergeOrder[i] })
		for _, idx := range mergeOrder {
			o := merges[idx]
			mustRecord(t, tr, o.name, o.in, o.out)
		}

		sigs := make(map[string]string)
		for i := 0; i < fanout; i++ {
			src := fmt.Sprintf("src%02d", i)
			d, err := tr.Downstream(ctx, rref(src, 1))
			if err != nil {
				t.Fatalf("Downstream(%s): %v", src, err)
			}
			sigs[src+":down"] = graphSignature(d)
		}
		for i := 0; i+1 < fanout; i++ {
			merged := fmt.Sprintf("merged%02d", i)
			u, err := tr.Upstream(ctx, rref(merged, 1))
			if err != nil {
				t.Fatalf("Upstream(%s): %v", merged, err)
			}
			sigs[merged+":up"] = graphSignature(u)
		}
		// 全图：从所有边导出签名（通过一个汇聚视图不易构造，这里逐源合并边集合签名）。
		return sigs
	}

	base := make([]int, len(layer1))
	for i := range base {
		base[i] = i
	}
	want := build(base)

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 5; trial++ {
		order := append([]int(nil), base...)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		got := build(order)
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("trial %d key %s:\n got %s\nwant %s", trial, k, got[k], v)
			}
		}
	}
}
