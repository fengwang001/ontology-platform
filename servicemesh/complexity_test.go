package servicemesh

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// 本文件以“可验证”的方式证明性能结论，包含三种证据：
//  1. 枚举计数器（探针）：一次请求实际评估的匹配项数量，与规则总数对拍；
//  2. 倍增对照基准：规则数/端点数扩大 20 倍，单请求耗时基本不增长；
//  3. Benchmark：go test -bench 的标准量化结果。

// probeCount 统计一次 lookup 中经过候选枚举与逐头校验的匹配项数。
// 通过重新执行索引内部同等的枚举路径得到，与真实路由一致。
func probeEvaluatedMatchers(idx *routeIndex, path string, headers map[string][]string) (candidates int, satisfied int) {
	clean := stripQuery(path)
	var segs []string
	if clean != "/" {
		segs = splitSegments(clean[1:])
	}
	node := idx.root
	seen := map[int]struct{}{}
	buf := make([]int, 0, 32)
	count := func(n *trieNode, isFinal bool) {
		buf = buf[:0]
		buf = append(buf, n.prefix...)
		for name, values := range headers {
			if vals := n.prefixHeaders.exact[name]; vals != nil {
				for _, v := range values {
					buf = append(buf, vals[v]...)
				}
			}
			if vt := n.prefixHeaders.prefix[name]; vt != nil {
				for _, v := range values {
					vt.enumerate(v, &buf)
				}
			}
			buf = append(buf, n.prefixHeaders.present[name]...)
		}
		if isFinal {
			buf = append(buf, n.exact...)
			for name, values := range headers {
				if vals := n.exactHeaders.exact[name]; vals != nil {
					for _, v := range values {
						buf = append(buf, vals[v]...)
					}
				}
				if vt := n.exactHeaders.prefix[name]; vt != nil {
					for _, v := range values {
						vt.enumerate(v, &buf)
					}
				}
				buf = append(buf, n.exactHeaders.present[name]...)
			}
		}
		for _, id := range buf {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			candidates++
			if matcherSatisfied(idx.matchers[id], headers) {
				satisfied++
			}
		}
	}
	count(node, len(segs) == 0)
	for i, seg := range segs {
		node = node.children[seg]
		if node == nil {
			break
		}
		count(node, i == len(segs)-1)
	}
	return
}

// buildUnrelatedRules 构造 n 条互不相关规则：
// 每条精确路径 /r/<i>，且要求唯一头 x-i: present；
// 另加一条“目标规则”精确路径 /hit，要求 x-hit 存在。
func buildUnrelatedRules(n int) *ServiceConfig {
	cfg := &ServiceConfig{
		Fallbacks: []Target{{Subset: "fb", Weight: 100}},
	}
	for i := 0; i < n; i++ {
		cfg.Rules = append(cfg.Rules, Rule{
			Matches: []MatchItem{{
				Path: PathMatch{Kind: PathExact, Path: "/r/" + strconv.Itoa(i)},
				Headers: []HeaderMatch{
					{Name: "x-" + strconv.Itoa(i), Op: HeaderPresent},
				},
			}},
			Targets: []Target{{Subset: "fb", Weight: 100}},
		})
	}
	cfg.Rules = append(cfg.Rules, Rule{
		Matches: []MatchItem{{
			Path:    PathMatch{Kind: PathExact, Path: "/hit"},
			Headers: []HeaderMatch{{Name: "x-hit", Op: HeaderPresent}},
		}},
		Targets: []Target{{Subset: "hit", Weight: 100}},
	})
	return cfg
}

func TestComplexityIndependentOfUnrelatedRules(t *testing.T) {
	l := newLogger(t)
	sizes := []int{1000, 5000, 20000}
	var prevCandidates int
	for _, n := range sizes {
		cfg := buildUnrelatedRules(n)
		compiled, e := compile(cfg)
		if e != nil {
			t.Fatal(e)
		}
		// 请求命中 /hit，与 /r/* 的 n 条规则全部无关。
		reqHeaders := map[string][]string{"x-hit": {"1"}}
		cands, sat := probeEvaluatedMatchers(compiled.index, "/hit", reqHeaders)
		hits := compiled.index.lookup("/hit", reqHeaders)
		if len(hits) != 1 || hits[0].ruleIdx != n {
			t.Fatalf("应只命中目标规则, got %d 个候选匹配", len(hits))
		}
		l.log("规则总数=%d 时请求 /hit：枚举候选=%d 满足=%d | 判定依据: 候选数不随规则总数增长",
			n+1, cands, sat)
		if cands > 4 { // /hit 节点上只有该规则自身；根节点无头匹配项
			t.Fatalf("枚举候选数 %d 应保持为小常数，与 %d 条无关规则无关", cands, n)
		}
		prevCandidates = cands
	}

	// 无命中请求：路径 /r/0 但不带任何头，n 条规则全部因头条件失败，
	// 枚举候选仍仅来自该路径所在 trie 节点（与 n 无关）。
	cfg := buildUnrelatedRules(20000)
	compiled, _ := compile(cfg)
	cands, sat := probeEvaluatedMatchers(compiled.index, "/r/0", map[string][]string{})
	l.log("%d 条规则下请求 /r/0 无头：枚举候选=%d 满足=%d（上一规模候选=%d）",
		20001, cands, sat, prevCandidates)
	if cands > 2 {
		t.Fatalf("无头条目枚举 %d 个候选，应只触及该 trie 节点上的条目", cands)
	}
}

func TestLatencyDoesNotGrowWithRulesOrEndpoints(t *testing.T) {
	l := newLogger(t)
	measure := func(nRules, nEndpoints int) time.Duration {
		m := NewMesh()
		cfg := buildUnrelatedRules(nRules)
		if _, e := m.Publish("svc", cfg, 0); e != nil {
			t.Fatal(e)
		}
		eps := make([]Endpoint, nEndpoints)
		for i := range eps {
			eps[i] = Endpoint{Name: "ep" + strconv.Itoa(i), Ready: true}
		}
		if e := m.RegisterSubsets("svc", map[string][]Endpoint{
			"hit": eps, "fb": {{Name: "fb", Ready: true}},
		}); e != nil {
			t.Fatal(e)
		}
		// 预热。
		for i := 0; i < 1000; i++ {
			m.Route("svc", Request{Path: "/hit",
				HeaderValues: map[string][]string{"x-hit": {"1"}}, Bucket: 0})
		}
		const N = 20000
		start := time.Now()
		for i := 0; i < N; i++ {
			_, e := m.Route("svc", Request{Path: "/hit",
				HeaderValues: map[string][]string{"x-hit": {"1"}}, Bucket: i % 10000})
			if e != nil {
				t.Fatal(e)
			}
		}
		return time.Since(start) / N
	}

	small := measure(1000, 10)
	big := measure(20000, 2000)
	l.log("单请求平均耗时: 1000规则/10端点=%v ; 20000规则/2000端点=%v (比值 %.2f) | 判定依据: 20倍规模下耗时基本不变",
		small, big, float64(big)/float64(small))
	// 允许噪声与 map 分配带来的有限放大，但绝不能随规模线性（线性应接近 20x）。
	if big > small*4 {
		t.Fatalf("开销随规模增长超阈值: small=%v big=%v", small, big)
	}
	fmt.Println("complexity ratio:", float64(big)/float64(small))
}

func BenchmarkRouteUnrelatedRules(b *testing.B) {
	m := NewMesh()
	cfg := buildUnrelatedRules(20000)
	if _, e := m.Publish("svc", cfg, 0); e != nil {
		b.Fatal(e)
	}
	m.RegisterSubsets("svc", map[string][]Endpoint{
		"hit": {{Name: "ep", Ready: true}},
		"fb":  {{Name: "fb", Ready: true}},
	})
	req := Request{Path: "/hit", HeaderValues: map[string][]string{"x-hit": {"1"}}, Bucket: 0}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, e := m.Route("svc", req); e != nil {
			b.Fatal(e)
		}
	}
}

func BenchmarkRouteNaiveScan(b *testing.B) {
	cfg := buildUnrelatedRules(20000)
	subsets := map[string]bool{"hit": true, "fb": true}
	headers := map[string][]string{"x-hit": {"1"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		naiveRoute(cfg, subsets, "/hit", headers, 0)
	}
}
