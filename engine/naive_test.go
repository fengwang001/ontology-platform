package engine_test

import (
	"fmt"
	"sort"
	"strings"

	"ontology/model"
)

// naive re-simulates the rules, recomputing reachability at every check.
type naive struct {
	n, end     int
	kind       []model.NodeType
	out, pred  [][]int
	act, fires map[int]int
	arr        map[[2]int]int
}

func newNaive(g *model.Graph, choices map[int][]int) *naive {
	s := &naive{n: g.N, kind: g.Kind, out: make([][]int, g.N+1), pred: make([][]int, g.N+1),
		act: map[int]int{}, arr: map[[2]int]int{}, fires: map[int]int{}}
	start := 0
	for v := 1; v <= g.N; v++ {
		outs := g.Out(v)
		if model.IsChoiceSplit(g.Kind[v]) {
			for _, i := range choices[v] {
				s.out[v] = append(s.out[v], outs[i].To)
			}
		} else {
			for _, e := range outs {
				s.out[v] = append(s.out[v], e.To)
			}
		}
		switch {
		case g.Kind[v] == model.Start:
			start = v
		case model.IsJoin(g.Kind[v]):
			s.fires[v] = 0
			for _, e := range g.In(v) {
				s.pred[v] = append(s.pred[v], e.From)
			}
		}
	}
	s.arrive(start, s.out[start][0])
	s.settle()
	return s
}

func (s *naive) clone() *naive {
	c := &naive{n: s.n, kind: s.kind, out: s.out, pred: s.pred, end: s.end,
		act: map[int]int{}, arr: map[[2]int]int{}, fires: map[int]int{}}
	for k, v := range s.act {
		c.act[k] = v
	}
	for k, v := range s.arr {
		c.arr[k] = v
	}
	for k, v := range s.fires {
		c.fires[k] = v
	}
	return c
}

func (s *naive) arrive(u, v int) {
	switch s.kind[v] {
	case model.Task:
		s.act[v]++
	case model.AndSplit, model.XorSplit, model.OrSplit:
		for _, w := range s.out[v] {
			s.arrive(v, w)
		}
	case model.End:
		s.end++
	default:
		s.arr[[2]int{u, v}]++
	}
}

func (s *naive) reachable(from, to int) bool {
	seen := map[int]bool{from: true}
	queue := []int{from}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, w := range s.out[u] {
			if w == to {
				return true
			}
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	return false
}

func (s *naive) arrSum(j int) int {
	sum := 0
	for _, u := range s.pred[j] {
		sum += s.arr[[2]int{u, j}]
	}
	return sum
}

func (s *naive) blocked(j int) bool {
	for t := range s.act {
		if s.reachable(t, j) {
			return true
		}
	}
	for o := 1; o <= s.n; o++ {
		if o != j && model.IsJoin(s.kind[o]) && s.arrSum(o) > 0 && s.reachable(o, j) {
			return true
		}
	}
	return false
}

func (s *naive) settle() {
	for {
		fired := false
		for j := 1; j <= s.n; j++ {
			if !model.IsJoin(s.kind[j]) {
				continue
			}
			if s.kind[j] == model.AndJoin {
				ready := len(s.pred[j]) > 0
				for _, u := range s.pred[j] {
					ready = ready && s.arr[[2]int{u, j}] > 0
				}
				if !ready {
					continue
				}
				for _, u := range s.pred[j] {
					k := [2]int{u, j}
					if s.arr[k]--; s.arr[k] == 0 {
						delete(s.arr, k)
					}
				}
			} else {
				if s.arrSum(j) == 0 || s.blocked(j) {
					continue
				}
				for _, u := range s.pred[j] {
					delete(s.arr, [2]int{u, j})
				}
			}
			s.fires[j]++
			s.arrive(j, s.out[j][0])
			fired = true
		}
		if !fired {
			return
		}
	}
}

func (s *naive) complete(t int) {
	if s.act[t]--; s.act[t] == 0 {
		delete(s.act, t)
	}
	s.arrive(t, s.out[t][0])
	s.settle()
}

func (s *naive) key() string {
	return fmt.Sprintf("end=%d act=%s arr=%s fires=%s", s.end, mapStr(s.act), arrStr(s.arr), mapStr(s.fires))
}

func mapStr(m map[int]int) string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%d:%d ", k, m[k])
	}
	return b.String()
}

func arrStr(m map[[2]int]int) string {
	keys := make([][2]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i][0]*100+keys[i][1] < keys[j][0]*100+keys[j][1]
	})
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%v:%d ", k, m[k])
	}
	return b.String()
}
