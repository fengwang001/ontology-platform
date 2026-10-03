package hekaton

import (
	"fmt"
	"reflect"
	"sort"
)

func sortedSet(s map[int]struct{}) []int {
	r := make([]int, 0, len(s))
	for k := range s {
		r = append(r, k)
	}
	sort.Ints(r)
	return r
}

// op 是一条随机调用。kind: b/r/w/p/f/a
type op struct {
	kind   byte
	t, key int
	x      int
}

func (o op) String() string {
	switch o.kind {
	case 'b':
		return "Begin()"
	case 'r':
		return fmt.Sprintf("Read(t=%d,k=%d)", o.t, o.key)
	case 'w':
		return fmt.Sprintf("Write(t=%d,k=%d,x=%d)", o.t, o.key, o.x)
	case 'p':
		return fmt.Sprintf("Precommit(t=%d)", o.t)
	case 'f':
		return fmt.Sprintf("Finish(t=%d)", o.t)
	case 'a':
		return fmt.Sprintf("Abort(t=%d)", o.t)
	}
	return "?"
}

func resultsEqual(a, b refResult) bool {
	if a.rejected != b.rejected || a.val != b.val {
		return false
	}
	la, lb := a.list, b.list
	if la == nil {
		la = []int{}
	}
	if lb == nil {
		lb = []int{}
	}
	return reflect.DeepEqual(la, lb)
}

func runEngine(e *Engine, o op) refResult {
	switch o.kind {
	case 'b':
		return refResult{val: e.Begin()}
	case 'r':
		v, err := e.Read(o.t, o.key)
		if err != nil {
			return refResult{rejected: true}
		}
		return refResult{val: v}
	case 'w':
		l, err := e.Write(o.t, o.key, o.x)
		if err != nil {
			return refResult{rejected: true}
		}
		return refResult{list: l}
	case 'p':
		v, err := e.Precommit(o.t)
		if err != nil {
			return refResult{rejected: true}
		}
		return refResult{val: v}
	case 'f':
		l, err := e.Finish(o.t)
		if err != nil {
			return refResult{rejected: true}
		}
		return refResult{list: l}
	case 'a':
		l, err := e.Abort(o.t)
		if err != nil {
			return refResult{rejected: true}
		}
		return refResult{list: l}
	}
	return refResult{rejected: true}
}

func runNaive(m *naive, o op) refResult {
	switch o.kind {
	case 'b':
		return m.begin()
	case 'r':
		return m.read(o.t, o.key)
	case 'w':
		return m.write(o.t, o.key, o.x)
	case 'p':
		return m.precommit(o.t)
	case 'f':
		return m.finish(o.t)
	case 'a':
		return m.abortOp(o.t)
	}
	return refResult{rejected: true}
}

// snapshot 是两模型的完整状态指纹（时钟/事务态/依赖/版本链）。
type snapshot struct {
	clock int
	txns  map[string]string
	vers  map[int]string
}

func engineSnap(e *Engine) snapshot {
	s := snapshot{clock: e.clock, txns: map[string]string{}, vers: map[int]string{}}
	for id, tr := range e.txns {
		s.txns[fmt.Sprintf("%d", id)] = fmt.Sprintf("%d/%d/%d/%v/d%v/dp%v",
			tr.state, tr.rt, tr.et, tr.finished,
			sortedSet(tr.deps), sortedSet(tr.dependents))
	}
	for k, vs := range e.keys {
		p := ""
		for _, v := range vs {
			p += fmt.Sprintf("(%d,c%d,e%d)", v.value, v.creator, v.ender)
		}
		s.vers[k] = p
	}
	return s
}

func naiveSnap(m *naive) snapshot {
	s := snapshot{clock: m.clock, txns: map[string]string{}, vers: map[int]string{}}
	for id, tr := range m.txns {
		s.txns[fmt.Sprintf("%d", id)] = fmt.Sprintf("%d/%d/%d/%v/d%v/dp%v",
			tr.state, tr.rt, tr.et, tr.finished,
			sortedBools(tr.deps), sortedBools(tr.dependTs))
	}
	for k, vs := range m.vers {
		p := ""
		for _, v := range vs {
			p += fmt.Sprintf("(%d,c%d,e%d)", v.val, v.creator, v.ender)
		}
		s.vers[k] = p
	}
	return s
}
