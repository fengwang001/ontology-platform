// Package mrg 在 run 的有序块上做多路归并与排序归并连接，仅依赖 run 包。
package mrg

import "errors"
import "math/bits"
import "ontology/run"

var ErrBadFanIn, ErrFanInExceeded = errors.New("mrg: max fan-in must be >= 2"), errors.New("mrg: number of runs exceeds max fan-in")

type Pair struct{ RKey, SKey run.Key }
type Step struct {
	RKey, SKey run.Key
	Action     string // 推进R/推进S/相等成对/结束
	Out        []Pair
}
type cursor struct {
	keys    []run.Key
	pos, id int
}
type node struct {
	h     []*cursor
	probe int // 非导出：最近一次 popMin 内 less 比较数，即取最小检查过的 run 首元素个数
}

func newNode(rs []run.Run) *node {
	n := &node{h: make([]*cursor, 0, len(rs))}
	for i := range rs {
		n.h = append(n.h, &cursor{keys: rs[i].Keys, id: i})
	}
	for i := len(n.h)/2 - 1; i >= 0; i-- {
		n.down(i)
	}
	return n
}
func (n *node) less(a, b int) bool {
	n.probe++
	x, y := n.h[a], n.h[b]
	ka, kb := x.keys[x.pos], y.keys[y.pos]
	return ka < kb || ka == kb && x.id < y.id // 键并列：编号最小（最早溢写）的 run 先出
}
func (n *node) down(i int) {
	for l := 2*i + 1; l < len(n.h); l = 2*i + 1 {
		s := l
		if r := l + 1; r < len(n.h) && n.less(r, l) {
			s = r
		}
		if !n.less(s, i) {
			return
		}
		n.h[i], n.h[s], i = n.h[s], n.h[i], s
	}
}
func (n *node) popMin() (run.Key, bool) {
	n.probe = 0
	if len(n.h) == 0 {
		return 0, false
	}
	c := n.h[0]
	k := c.keys[c.pos]
	c.pos++
	if c.pos == len(c.keys) {
		n.h[0], n.h = n.h[len(n.h)-1], n.h[:len(n.h)-1]
	}
	n.down(0)
	return k, true
}
func (n *node) head() (run.Key, bool) {
	if len(n.h) == 0 {
		return -1, false
	}
	return n.h[0].keys[n.h[0].pos], true
}
func drain(n *node, k run.Key) int { // 弹出并统计连续等于 k 的元素个数
	c := 0
	for x, ok := n.head(); ok && x == k; x, ok = n.head() {
		c++
		n.popMin()
	}
	return c
}
func fanErr(fanIn, nr, ns int) error {
	if fanIn < 2 {
		return ErrBadFanIn
	}
	if nr > fanIn || ns > fanIn {
		return ErrFanInExceeded
	}
	return nil
}
func Merge(rs []run.Run, fanIn int) ([]run.Key, error) {
	if err := fanErr(fanIn, len(rs), 0); err != nil {
		return nil, err
	}
	n := newNode(rs)
	out := make([]run.Key, 0)
	for k, ok := n.popMin(); ok; k, ok = n.popMin() {
		out = append(out, k)
	}
	return out, nil
}
func Join(rr, ss []run.Run, fanIn int) ([]Pair, []Step, error) {
	if err := fanErr(fanIn, len(rr), len(ss)); err != nil {
		return nil, nil, err
	}
	rn, sn := newNode(rr), newNode(ss)
	pairs, steps := []Pair(nil), []Step(nil)
	for {
		rk, rok := rn.head()
		sk, sok := sn.head()
		if !rok || !sok {
			if rok || sok {
				steps = append(steps, Step{RKey: rk, SKey: sk, Action: "结束"})
			}
			return pairs, steps, nil
		}
		switch {
		case rk < sk:
			rn.popMin()
			steps = append(steps, Step{RKey: rk, SKey: sk, Action: "推进R"})
		case rk > sk:
			sn.popMin()
			steps = append(steps, Step{RKey: rk, SKey: sk, Action: "推进S"})
		default:
			rn.popMin()
			sn.popMin()
			cr, cs := 1+drain(rn, rk), 1+drain(sn, sk)
			out := make([]Pair, cr*cs)
			for t := range out {
				out[t] = Pair{RKey: rk, SKey: sk}
			}
			pairs = append(pairs, out...)
			steps = append(steps, Step{RKey: rk, SKey: sk, Action: "相等成对", Out: out})
		}
	}
}
func ProbeBoundHolds() bool {
	for _, m := range []int{100, 1000, 10000} {
		rs := make([]run.Run, m)
		for i := range rs {
			rs[i] = run.Run{Keys: []run.Key{run.Key(i)}}
		}
		n := newNode(rs)
		for c, h := 0, bits.Len(uint(m-1)); c < m; c++ {
			if _, ok := n.popMin(); !ok || n.probe > 2*h+2 {
				return false
			}
		}
	}
	return true
}
