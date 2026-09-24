package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/crit"
)

type node struct {
	n string
	l int64
}

func ok(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func buildP(t *testing.T, ns []node, es [][2]string) *Pipeline {
	p := New()
	for _, x := range ns {
		ok(t, p.Add(x.n, x.l))
	}
	for _, e := range es {
		ok(t, p.Link(e[0], e[1]))
	}
	return p
}

var (
	e5     = [][2]string{{"S", "A"}, {"S", "B"}, {"A", "A2"}, {"B", "T"}, {"A2", "T"}}
	fiveN  = []node{{"S", 0}, {"A", 30}, {"B", 50}, {"A2", 30}, {"T", 0}}
	fiveB0 = []node{{"S", 0}, {"A", 30}, {"B", 0}, {"A2", 30}, {"T", 0}}
	tieN   = []node{{"S", 0}, {"z", 40}, {"a", 40}, {"T", 0}}
	tieE   = [][2]string{{"S", "z"}, {"S", "a"}, {"z", "T"}, {"a", "T"}}
)

// 一张表钉住不变量 1（DP 与暴力枚举一致）与 2（瓶颈正确、落关键路径、并列字典序）。
func TestEndToEndAndBottleneck(t *testing.T) {
	cases := []struct {
		name    string
		ns      []node
		es      [][2]string
		e2e     int64
		bn      string
		bnLat   int64
		notOnCP string
	}{
		{"five: A(30) beats global B(50)", fiveN, e5, 60, "A", 30, "B"},
		{"five with B=0 unchanged", fiveB0, e5, 60, "A", 30, "B"},
		{"lexicographic tie", tieN, tieE, 40, "a", 40, ""},
		{"chain", []node{{"S", 1}, {"m", 2}, {"T", 3}}, [][2]string{{"S", "m"}, {"m", "T"}}, 6, "T", 3, ""},
		{"zero lats", []node{{"S", 0}, {"x", 0}, {"T", 0}}, [][2]string{{"S", "x"}, {"x", "T"}}, 0, "S", 0, ""},
		{"diamond", []node{{"S", 1}, {"a", 2}, {"b", 9}, {"c", 3}, {"T", 1}},
			[][2]string{{"S", "a"}, {"S", "b"}, {"a", "c"}, {"b", "c"}, {"c", "T"}}, 14, "b", 9, "a"},
	}
	for _, c := range cases {
		p := buildP(t, c.ns, c.es)
		e, err := p.EndToEnd()
		if err != nil || e != c.e2e {
			t.Fatalf("%s: EndToEnd=%d err=%v want %d", c.name, e, err, c.e2e)
		}
		brute, err := crit.BruteEndToEnd(p.g)
		if err != nil || brute != e {
			t.Fatalf("%s: brute=%d want EndToEnd=%d", c.name, brute, e)
		}
		bn, l, err := p.Bottleneck()
		if err != nil || bn != c.bn || l != c.bnLat {
			t.Fatalf("%s: Bottleneck=(%s,%d) want (%s,%d)", c.name, bn, l, c.bn, c.bnLat)
		}
		if r, err := crit.Analyze(p.g); err != nil || !r.OnCriticalPath[bn] ||
			(c.notOnCP != "" && r.OnCriticalPath[c.notOnCP]) {
			t.Fatalf("%s: critical-path membership wrong bn=%s notOnCP=%s", c.name, bn, c.notOnCP)
		}
	}
}

// 不变量 4：四类被拒操作各自报错、哨兵两两不同，拒绝后状态原样。
func TestErrors_StatePreserved(t *testing.T) {
	cases := []struct {
		name string
		op   func(p *Pipeline) error
		want error
	}{
		{"duplicate add", func(p *Pipeline) error { return p.Add("S", 9) }, ErrDuplicateOperator},
		{"negative lat", func(p *Pipeline) error { return p.Add("x", -1) }, ErrNegativeLatency},
		{"unknown from", func(p *Pipeline) error { return p.Link("X", "T") }, ErrUnknownOperator},
		{"unknown to", func(p *Pipeline) error { return p.Link("S", "X") }, ErrUnknownOperator},
		{"self loop", func(p *Pipeline) error { return p.Link("S", "S") }, ErrCycle},
		{"cycle", func(p *Pipeline) error { return p.Link("T", "S") }, ErrCycle},
	}
	for _, c := range cases {
		p := buildP(t, []node{{"S", 0}, {"T", 0}}, [][2]string{{"S", "T"}})
		if err := c.op(p); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if e, err := p.EndToEnd(); err != nil || e != 0 {
			t.Fatalf("%s: state changed after rejection e2e=%d err=%v", c.name, e, err)
		}
	}
	sentinels := []error{ErrDuplicateOperator, ErrUnknownOperator, ErrCycle, ErrNegativeLatency}
	for i := 0; i < len(sentinels); i++ { // 四类哨兵两两不同
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %d and %d are equal", i, j)
			}
		}
	}
}
func TestBadTopologyAndSelfCheck(t *testing.T) {
	p := New()
	ok(t, p.Add("a", 1))
	ok(t, p.Add("b", 1)) // 两个孤立节点：2 源 2 汇
	if _, err := p.EndToEnd(); !errors.Is(err, ErrIllegalTopology) {
		t.Fatalf("err=%v want ErrIllegalTopology", err)
	}
	ok(t, New().SelfCheck())
}
func TestConcurrentReaders(t *testing.T) {
	p := buildP(t, fiveN, e5)
	const n = 64
	got := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			e, _ := p.EndToEnd()
			bn, l, _ := p.Bottleneck()
			if err := p.SelfCheck(); err != nil {
				t.Error(err)
			}
			got[i] = fmt.Sprintf("%d|%s|%d", e, bn, l) // 逐字段拼接，逐字段比较
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatalf("reader %d=%q want %q", i, got[i], got[0])
		}
	}
}
