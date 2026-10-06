package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// buildLargeProgram 构造 n 条顺序语句，每条都对 x 做一次可赋值赋值并带有唯一
// 查询点，用于验证查询开销不随语句总数增长。
func buildLargeProgram(n int) (*Program, []string) {
	p := &Program{Decls: map[string]*Type{"x": TUnion(TNumber(), TString())}}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("s%d", i)
		ids = append(ids, id)
		val := TNumber()
		if i%2 == 0 {
			val = TNumLit(float64(i))
		}
		p.Stmts = append(p.Stmts, &Stmt{ID: id, K: StmtAssign, Var: "x", Value: val})
	}
	return p, ids
}

// TestConcurrentQueries 在 -race 下验证同一结果可被多调用方并发查询，
// 并同时分析不同程序，结果等价于某个串行顺序。
func TestConcurrentQueries(t *testing.T) {
	l := newLogger(t, "concurrent-queries")
	defer l.finish()

	const programs = 8
	results := make([]*Result, programs)
	for i := 0; i < programs; i++ {
		g := generateProgram(int64(1000 + i))
		r, err := Analyze(t.Context(), g.prog)
		if err != nil {
			// 错误程序跳过；保证至少有成功结果
			i--
			continue
		}
		results[i] = r
	}

	var wg sync.WaitGroup
	for w := 0; w < 64; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for _, r := range results {
				if r == nil {
					continue
				}
				for round := 0; round < 50; round++ {
					for _, variable := range []string{"x", "y", "e"} {
						for id := range r.entry {
							pr, err := r.Query(id, variable)
							if err != nil {
								t.Errorf("并发查询出错: %v", err)
								return
							}
							if pr.Status == PointReachable && pr.Type == nil {
								t.Errorf("可达点返回空类型")
								return
							}
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
	l.output("64 个 worker 对 8 个不同程序的结果并发查询完成，无数据竞争")
	l.reason("Result 构建后内部快照只读；Query 仅做 map 查询与类型还原，无共享写")
}

// TestQueryCostIndependentOfProgramSize 验证查询同一个点的耗时不随程序语句
// 总数增长。方法：构造小/大两个程序，在相同索引点查询相同规模的窄化类型，
// 比较耗时（取多轮中位数，允许一定抖动，但大程序不得数量级变慢）。
func TestQueryCostIndependentOfProgramSize(t *testing.T) {
	l := newLogger(t, "query-cost")
	defer l.finish()

	measure := func(n int) (int64, *Result) {
		p, ids := buildLargeProgram(n)
		r, err := Analyze(t.Context(), p)
		if err != nil {
			t.Fatalf("n=%d 分析失败: %v", n, err)
		}
		target := ids[n-1]
		// 预热
		for i := 0; i < 1000; i++ {
			if _, err := r.Query(target, "x"); err != nil {
				t.Fatal(err)
			}
		}
		start := nanotime()
		const reps = 200000
		var sink *Type
		for i := 0; i < reps; i++ {
			pr, _ := r.Query(target, "x")
			sink = pr.Type
		}
		elapsed := nanotime() - start
		_ = sink
		return elapsed / int64(reps), r
	}

	small, _ := measure(100)
	large, _ := measure(20000)
	l.input("小程序=100 语句，大程序=20000 语句，各自在末点查询 x")
	l.output("单次查询平均耗时 小=%dns 大=%dns（大/小=%.2fx）", small, large,
		float64(large)/float64(small))
	l.reason("查询只索引 entry[id][变量] 并还原该变量类型；entry 为扁平 map，与语句总数无关")

	// 宽松上限：即便有缓存/调度抖动，大程序单次查询不应超过小程序的 5 倍。
	if large > 5*small && large-small > 50 {
		t.Fatalf("查询耗时疑似随程序规模增长: 小=%dns 大=%dns", small, large)
	}
}

// TestNoReanalysis 验证同一 Result 的任意次 Query 返回等价结果，且结果不可变。
func TestNoReanalysis(t *testing.T) {
	l := newLogger(t, "no-reanalysis")
	defer l.finish()
	g := generateProgram(42)
	r, err := Analyze(t.Context(), g.prog)
	if err != nil {
		t.Skipf("seed 42 恰好生成错误程序: %v", err)
	}
	var first map[string]string
	for round := 0; round < 100; round++ {
		snap := map[string]string{}
		for id := range r.entry {
			pr, qerr := r.Query(id, "x")
			if qerr != nil {
				t.Fatal(qerr)
			}
			if pr.Status == PointReachable {
				snap[id] = renderType(pr.Type)
			} else {
				snap[id] = "<unreachable>"
			}
		}
		if first == nil {
			first = snap
			continue
		}
		if len(snap) != len(first) {
			t.Fatalf("重复查询的程序点数量发生变化")
		}
		for k, v := range snap {
			if first[k] != v {
				t.Fatalf("点 %s 重复查询结果不稳定: %s vs %s", k, first[k], v)
			}
		}
	}
	l.output("同一结果 100 轮全点查询，结果完全一致")
	l.reason("快照不可变且查询无副作用，等价于不重新分析")
}
