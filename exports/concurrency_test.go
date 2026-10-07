package exports

import (
	"sync"
	"testing"
)

// TestConcurrentSwapAndResolve 并发执行表替换与解析，
// 每次解析的结果必须完整地属于旧表或新表之一。
func TestConcurrentSwapAndResolve(t *testing.T) {
	tableA := []Entry{
		{Key: ".", Target: StringTarget("./a/index.js")},
		{Key: "./*", Target: StringTarget("./a/lib/*")},
		{Key: "./exact", Target: ConditionsTarget(
			Cond("node", StringTarget("./a/node.js")),
			Cond("default", StringTarget("./a/default.js")),
		)},
	}
	tableB := []Entry{
		{Key: ".", Target: StringTarget("./b/index.js")},
		{Key: "./*", Target: StringTarget("./b/lib/*")},
		{Key: "./exact", Target: ConditionsTarget(
			Cond("node", StringTarget("./b/node.js")),
			Cond("default", StringTarget("./b/default.js")),
		)},
	}
	r := mustResolver(t, tableA)

	// 每个请求在两表下的合法结果集合（以目标字符串区分表的归属）。
	type request struct {
		subpath string
		conds   []string
	}
	requests := []request{
		{".", nil},
		{"./x/y", nil},
		{"./exact", []string{"node"}},
		{"./exact", nil},
	}
	validFor := func(req request) map[string]string {
		// 目标字符串 -> 键，两张表各算一次。
		out := make(map[string]string)
		for _, entries := range [][]Entry{tableA, tableB} {
			single, err := NewResolver(entries)
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			res, err := single.Resolve(req.subpath, req.conds)
			if err != nil {
				t.Fatalf("基准解析失败: %v", err)
			}
			out[res.Target] = res.Key
		}
		return out
	}
	validSets := make([]map[string]string, len(requests))
	for i, req := range requests {
		validSets[i] = validFor(req)
	}

	const workers = 8
	const iterations = 2000
	stop := make(chan struct{})
	var swapWg sync.WaitGroup
	var resolveWg sync.WaitGroup

	// 换表协程：在两表之间来回原子替换。
	swapWg.Add(1)
	go func() {
		defer swapWg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				if err := r.Replace(tableA); err != nil {
					t.Errorf("Replace(tableA): %v", err)
					return
				}
			} else {
				if err := r.Replace(tableB); err != nil {
					t.Errorf("Replace(tableB): %v", err)
					return
				}
			}
			i++
		}
	}()

	// 解析协程：结果必须完整属于某一表。
	for w := 0; w < workers; w++ {
		resolveWg.Add(1)
		go func(id int) {
			defer resolveWg.Done()
			for i := 0; i < iterations; i++ {
				req := requests[(id+i)%len(requests)]
				res, err := r.Resolve(req.subpath, req.conds)
				if err != nil {
					t.Errorf("Resolve(%q, %v): %v", req.subpath, req.conds, err)
					return
				}
				key, ok := validSets[(id+i)%len(requests)][res.Target]
				if !ok || key != res.Key {
					t.Errorf("结果混用了两张表: req=%+v res=%+v", req, res)
					return
				}
			}
		}(w)
	}

	// 解析协程全部结束后停止换表协程。
	resolveWg.Wait()
	close(stop)
	swapWg.Wait()
}
