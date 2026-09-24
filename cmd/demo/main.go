// Command demo 逐条打印算子延迟图的判定，退出码 0 表示全部 OK；不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/crit"
	"ontology/dag"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

type nd struct {
	n string
	l int64
}

var e5 = [][2]string{{"S", "A"}, {"S", "B"}, {"A", "A2"}, {"B", "T"}, {"A2", "T"}}
var five = []nd{{"S", 0}, {"A", 30}, {"B", 50}, {"A2", 30}, {"T", 0}}

func graphs(nodes []nd, edges [][2]string) (*api.Pipeline, *dag.Graph) {
	p, g := api.New(), dag.New()
	for _, x := range nodes {
		if err := p.Add(x.n, x.l); err != nil {
			panic(err)
		}
		if err := g.Add(x.n, x.l); err != nil {
			panic(err)
		}
	}
	for _, e := range edges {
		if err := p.Link(e[0], e[1]); err != nil {
			panic(err)
		}
		if err := g.Link(e[0], e[1]); err != nil {
			panic(err)
		}
	}
	return p, g
}

func main() {
	p, g := graphs(five, e5)
	sol, err := g.Solve()
	if err != nil {
		fmt.Println("FAIL solve:", err)
		os.Exit(1)
	}
	d := sol.Dist
	check(fmt.Sprintf("dist rows S=%d A=%d B=%d A2=%d T=%d", d["S"], d["A"], d["B"], d["A2"], d["T"]),
		d["S"] == 0 && d["A"] == 30 && d["B"] == 50 && d["A2"] == 60 && d["T"] == 60)

	e, _ := p.EndToEnd()
	bn, bl, _ := p.Bottleneck()
	brute, _ := crit.BruteEndToEnd(g)
	check(fmt.Sprintf("EndToEnd=%d Bottleneck=%s(%d) brute=%d", e, bn, bl, brute),
		e == 60 && bn == "A" && bl == 30 && brute == 60)

	pt, _ := graphs([]nd{{"S", 0}, {"z", 40}, {"a", 40}, {"T", 0}},
		[][2]string{{"S", "z"}, {"S", "a"}, {"z", "T"}, {"a", "T"}})
	tbn, _, _ := pt.Bottleneck()
	check("bottleneck tie lexicographic -> "+tbn, tbn == "a")

	// 四类可判定错误互不相同。
	bad := []error{
		api.New().Add("x", -1),
		func() error { q := api.New(); _ = q.Add("S", 0); return q.Add("S", 1) }(),
		func() error { q := api.New(); _ = q.Add("S", 0); return q.Link("X", "S") }(),
		func() error { q := api.New(); _ = q.Add("S", 0); return q.Link("S", "S") }(),
	}
	want := []error{api.ErrNegativeLatency, api.ErrDuplicateOperator, api.ErrUnknownOperator, api.ErrCycle}
	distinct := true
	for i := range bad {
		if !errors.Is(bad[i], want[i]) {
			distinct = false
		}
		for j := i + 1; j < len(bad); j++ {
			if errors.Is(bad[i], bad[j]) {
				distinct = false
			}
		}
	}
	check("four distinct decidable errors", distinct)

	// 被拒后状态不变、仍可正常使用。
	q, _ := graphs([]nd{{"S", 0}, {"T", 0}}, [][2]string{{"S", "T"}})
	before, _ := q.EndToEnd()
	_ = q.Link("T", "S") // 成环，应被拒
	after, _ := q.EndToEnd()
	usable := q.Add("U", 5) == nil && q.Link("S", "U") == nil && q.Link("U", "T") == nil
	again, _ := q.EndToEnd()
	check("rejected op leaves no trace", before == after && after == 0 && usable && again == 5)

	// 大 m 链多档均线性可解（精确松弛次数 m-1 由 dag 包内测试钉死，计数器不经公开接口暴露）。
	linear := true
	for _, m := range []int{100, 1000, 10000} {
		big := api.New()
		for i := 0; i < m; i++ {
			if err := big.Add(fmt.Sprintf("n%d", i), 1); err != nil {
				panic(err)
			}
		}
		for i := 0; i+1 < m; i++ {
			if err := big.Link(fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1)); err != nil {
				panic(err)
			}
		}
		if v, err := big.EndToEnd(); err != nil || v != int64(m) {
			linear = false
		}
	}
	check("large-m chain linear across 100/1000/10000", linear)

	// 并发只读，逐字段一致；无 sleep。
	const n = 32
	res := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			v, _ := p.EndToEnd()
			b, l, _ := p.Bottleneck()
			res[i] = fmt.Sprintf("%d|%s|%d", v, b, l)
		}(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && res[i] == res[0]
	}
	check("concurrent readers agree", same)
	check("SelfCheck", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
