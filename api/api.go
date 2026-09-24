// Package api 是算子延迟 DAG 的对外门面：构图、端到端延迟与瓶颈查询、自检；依赖方向 api->crit->dag。
package api

import (
	"errors"
	"fmt"

	"ontology/crit"
	"ontology/dag"
)

// 可判定哨兵错误：前四类互不相同，第五类是非法拓扑（源/汇数目不为 1）。
var ErrDuplicateOperator = dag.ErrDuplicateNode
var ErrUnknownOperator = dag.ErrUnknownNode
var ErrCycle = dag.ErrCycle
var ErrNegativeLatency = dag.ErrNegativeLat
var ErrIllegalTopology = dag.ErrBadTopology

type Pipeline struct{ g *dag.Graph }

func New() *Pipeline { return &Pipeline{g: dag.New()} }

func (p *Pipeline) Add(name string, lat int64) error { return p.g.Add(name, lat) }

func (p *Pipeline) Link(from, to string) error { return p.g.Link(from, to) }

// EndToEnd 返回最长源→汇路径（关键路径）上算子延迟之和（含源汇自身 lat）。
func (p *Pipeline) EndToEnd() (int64, error) {
	sol, err := p.g.Solve()
	if err != nil {
		return 0, err
	}
	return sol.EndToEnd, nil
}

// Bottleneck 返回关键路径上 lat 最大的算子；并列时取名字典序最小者。
func (p *Pipeline) Bottleneck() (string, int64, error) {
	r, err := crit.Analyze(p.g)
	if err != nil {
		return "", 0, err
	}
	return r.Bottleneck, r.BottleneckLat, nil
}

type ns struct {
	n string
	l int64
}

func mk(nodes []ns, edges [][2]string) *Pipeline {
	p := New()
	for _, x := range nodes {
		if err := p.Add(x.n, x.l); err != nil {
			panic(err)
		}
	}
	for _, e := range edges {
		if err := p.Link(e[0], e[1]); err != nil {
			panic(err)
		}
	}
	return p
}

// SelfCheck 核验四不变量：DP 一致 / 与暴力枚举一致 / 瓶颈正确 / 失败不留痕。
func (p *Pipeline) SelfCheck() error {
	e5 := [][2]string{{"S", "A"}, {"S", "B"}, {"A", "A2"}, {"B", "T"}, {"A2", "T"}}
	cases := []struct {
		ns    []ns
		edges [][2]string
		e2e   int64
		bn    string
	}{
		{[]ns{{"S", 0}, {"A", 30}, {"B", 50}, {"A2", 30}, {"T", 0}}, e5, 60, "A"},
		{[]ns{{"S", 0}, {"z", 40}, {"a", 40}, {"T", 0}}, [][2]string{{"S", "z"}, {"S", "a"}, {"z", "T"}, {"a", "T"}}, 40, "a"},
		{[]ns{{"S", 1}, {"m", 2}, {"T", 3}}, [][2]string{{"S", "m"}, {"m", "T"}}, 6, "T"},
	}
	for _, c := range cases {
		q := mk(c.ns, c.edges)
		sol, err := q.g.Solve()
		if err != nil {
			return err
		}
		ind, err := crit.IndependentDist(q.g) // 不变量 3：距离 DP 一致
		if err != nil {
			return err
		}
		for n, d := range ind {
			if d != sol.Dist[n] {
				return fmt.Errorf("selfcheck dist[%s]=%d want %d", n, sol.Dist[n], d)
			}
		}
		e, err := q.EndToEnd()
		if err != nil {
			return err
		}
		b, err := crit.BruteEndToEnd(q.g) // 不变量 1：与朴素枚举一致
		if err != nil || e != c.e2e || e != b {
			return fmt.Errorf("selfcheck e2e=%d brute=%d want %d err=%v", e, b, c.e2e, err)
		}
		r, err := crit.Analyze(q.g) // 不变量 2：瓶颈落在关键路径且选取正确
		if err != nil || r.Bottleneck != c.bn || !r.OnCriticalPath[r.Bottleneck] {
			return fmt.Errorf("selfcheck bottleneck=%s want %s err=%v", r.Bottleneck, c.bn, err)
		}
	}
	return checkRejection() // 不变量 4：失败不留痕
}
func checkRejection() error {
	q := mk([]ns{{"S", 0}, {"T", 0}}, [][2]string{{"S", "T"}})
	bad := []func() error{
		func() error { return q.Add("S", 7) },
		func() error { return q.Add("bad", -1) },
		func() error { return q.Link("X", "T") },
		func() error { return q.Link("S", "X") },
		func() error { return q.Link("T", "S") },
	}
	for _, f := range bad {
		if f() == nil {
			return errors.New("selfcheck: rejected op unexpectedly succeeded")
		}
	}
	for _, x := range []ns{{"bad", 1}, {"U", 5}} { // “bad” 曾被负延迟拒绝，现在应能正常加入
		if err := q.Add(x.n, x.l); err != nil {
			return err
		}
	}
	for _, e := range [][2]string{{"S", "U"}, {"U", "T"}, {"S", "bad"}, {"bad", "T"}} {
		if err := q.Link(e[0], e[1]); err != nil { // 若 T->S 成环边曾留下，这里必报错
			return err
		}
	}
	if e, err := q.EndToEnd(); err != nil || e != 5 { // 重名改 lat 或成环都会让它偏离 5
		return fmt.Errorf("selfcheck: post-rejection state broken e2e=%d err=%v", e, err)
	}
	return nil
}
