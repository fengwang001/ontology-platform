package dirtyflush

import (
	"sort"
	"strings"
)

// 朴素模型：严格按题目规则逐步维护，链表用排序切片模拟，
// 依赖图用 map 模拟。与生产实现不共享任何代码。
type nPage struct {
	lsn        int64
	dirty      bool
	oldest     int64
	inFlight   bool
	firstAfter int64
	snap       int64
}

type naive struct {
	cap       int
	pages     map[int]*nPage
	out, in   map[int]map[int]struct{}
	maxModify int64
	flushed   int64
	log       *strings.Builder
}

func newNaive(D int) *naive {
	return &naive{
		cap:   D,
		pages: make(map[int]*nPage),
		out:   make(map[int]map[int]struct{}),
		in:    make(map[int]map[int]struct{}),
		log:   new(strings.Builder),
	}
}

func (n *naive) pg(p int) *nPage {
	st := n.pages[p]
	if st == nil {
		st = &nPage{}
		n.pages[p] = st
	}
	return st
}

func (n *naive) note(format string, args ...any) {
	if n.log != nil {
		n.log.WriteString("    ")
		writeFmt(n.log, format, args...)
		n.log.WriteByte('\n')
	}
}

type outcome struct {
	ok     bool
	reason RejectReason
	snap   int64
	plan   []int
	value  int64
}

func (n *naive) dirtySorted() []int {
	type kv struct {
		p      int
		oldest int64
	}
	var all []kv
	for p, st := range n.pages {
		if st.dirty {
			all = append(all, kv{p, st.oldest})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return keyLess(all[i].oldest, all[i].p, all[j].oldest, all[j].p)
	})
	out := make([]int, len(all))
	for i, x := range all {
		out[i] = x.p
	}
	return out
}

func (n *naive) reaches(from, to int) bool {
	seen := map[int]bool{from: true}
	stack := []int{from}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == to {
			return true
		}
		for y := range n.out[x] {
			if !seen[y] {
				seen[y] = true
				stack = append(stack, y)
			}
		}
	}
	return false
}

func (n *naive) do(op string, p1, p2 int, lsn int64) outcome {
	switch op {
	case "Modify":
		if p1 < 0 || p1 > MaxPage || lsn < MinLSN || lsn > MaxLSN {
			n.note("Modify(%d,%d) -> REJECT invalid", p1, lsn)
			return outcome{reason: ReasonInvalidArg}
		}
		if lsn <= n.maxModify {
			n.note("Modify(%d,%d) -> REJECT lsn-too-small (max=%d)", p1, lsn, n.maxModify)
			return outcome{reason: ReasonLSNTooSmall}
		}
		st := n.pg(p1)
		dirtyN := len(n.dirtySorted())
		if !st.dirty && dirtyN >= n.cap {
			n.note("Modify(%d,%d) -> REJECT dirty-full (%d/%d)", p1, lsn, dirtyN, n.cap)
			return outcome{reason: ReasonDirtyFull}
		}
		n.maxModify = lsn
		st.lsn = lsn
		switch {
		case !st.dirty:
			st.dirty = true
			st.oldest = lsn
			n.note("Modify(%d,%d) -> OK newly-dirty oldest=%d", p1, lsn, lsn)
		case st.inFlight:
			if st.firstAfter == 0 {
				st.firstAfter = lsn
			}
			n.note("Modify(%d,%d) -> OK in-flight firstAfter=%d", p1, lsn, st.firstAfter)
		default:
			n.note("Modify(%d,%d) -> OK stays-dirty oldest=%d", p1, lsn, st.oldest)
		}
		return outcome{ok: true}
	case "SetFlushed":
		if lsn < 0 || lsn > MaxLSN {
			n.note("SetFlushed(%d) -> REJECT invalid", lsn)
			return outcome{reason: ReasonInvalidArg}
		}
		if lsn < n.flushed {
			n.note("SetFlushed(%d) -> REJECT rollback (cur=%d)", lsn, n.flushed)
			return outcome{reason: ReasonLSNTooSmall}
		}
		n.flushed = lsn
		n.note("SetFlushed(%d) -> OK", lsn)
		return outcome{ok: true}
	case "FlushStart":
		if p1 < 0 || p1 > MaxPage {
			n.note("FlushStart(%d) -> REJECT invalid", p1)
			return outcome{reason: ReasonInvalidArg}
		}
		st := n.pg(p1)
		if !st.dirty {
			n.note("FlushStart(%d) -> REJECT not-dirty", p1)
			return outcome{reason: ReasonPageNotDirty}
		}
		if st.inFlight {
			n.note("FlushStart(%d) -> REJECT in-flight", p1)
			return outcome{reason: ReasonPageInFlight}
		}
		if st.lsn > n.flushed {
			n.note("FlushStart(%d) -> REJECT log-not-durable (lsn=%d flushed=%d)", p1, st.lsn, n.flushed)
			return outcome{reason: ReasonLogNotDurable}
		}
		var blockers []int
		for q := range n.in[p1] {
			if n.pg(q).dirty {
				blockers = append(blockers, q)
			}
		}
		if len(blockers) > 0 {
			sort.Ints(blockers)
			n.note("FlushStart(%d) -> REJECT pred-dirty blockers=%v", p1, blockers)
			return outcome{reason: ReasonPredecessorDirty}
		}
		st.inFlight = true
		st.snap = st.lsn
		n.note("FlushStart(%d) -> OK snap=%d", p1, st.snap)
		return outcome{ok: true, snap: st.snap}
	case "FlushDone":
		if p1 < 0 || p1 > MaxPage {
			n.note("FlushDone(%d) -> REJECT invalid", p1)
			return outcome{reason: ReasonInvalidArg}
		}
		st := n.pg(p1)
		if !st.inFlight {
			n.note("FlushDone(%d) -> REJECT not-in-flight", p1)
			return outcome{reason: ReasonNotInFlight}
		}
		if st.lsn == st.snap {
			st.dirty = false
			st.inFlight = false
			st.oldest = 0
			st.firstAfter = 0
			st.snap = 0
			for b := range n.out[p1] {
				delete(n.in[b], p1)
				if len(n.in[b]) == 0 {
					delete(n.in, b)
				}
			}
			delete(n.out, p1)
			n.note("FlushDone(%d) -> OK cleaned, out edges removed", p1)
		} else {
			st.inFlight = false
			st.oldest = st.firstAfter
			st.firstAfter = 0
			st.snap = 0
			n.note("FlushDone(%d) -> OK stays-dirty new-oldest=%d", p1, st.oldest)
		}
		return outcome{ok: true}
	case "AddDep":
		if p1 < 0 || p1 > MaxPage || p2 < 0 || p2 > MaxPage || p1 == p2 {
			n.note("AddDep(%d,%d) -> REJECT invalid", p1, p2)
			return outcome{reason: ReasonInvalidArg}
		}
		if !n.pg(p1).dirty {
			n.note("AddDep(%d,%d) -> OK-but-skip (a clean)", p1, p2)
			return outcome{ok: true}
		}
		if set := n.out[p1]; set != nil {
			if _, dup := set[p2]; dup {
				n.note("AddDep(%d,%d) -> OK idempotent", p1, p2)
				return outcome{ok: true}
			}
		}
		if n.reaches(p2, p1) {
			n.note("AddDep(%d,%d) -> REJECT cycle", p1, p2)
			return outcome{reason: ReasonCycle}
		}
		if n.out[p1] == nil {
			n.out[p1] = map[int]struct{}{}
		}
		n.out[p1][p2] = struct{}{}
		if n.in[p2] == nil {
			n.in[p2] = map[int]struct{}{}
		}
		n.in[p2][p1] = struct{}{}
		n.note("AddDep(%d,%d) -> OK registered", p1, p2)
		return outcome{ok: true}
	case "Checkpoint":
		var cp int64
		if all := n.dirtySorted(); len(all) > 0 {
			cp = n.pages[all[0]].oldest
		} else {
			cp = n.maxModify + 1
		}
		n.note("Checkpoint -> %d", cp)
		return outcome{ok: true, value: cp}
	case "Plan":
		if lsn < 1 {
			n.note("Plan(%d) -> REJECT invalid", lsn)
			return outcome{reason: ReasonInvalidArg}
		}
		chain := n.dirtySorted()
		sched := map[int]bool{}
		var plan []int
		var emit func(int)
		emit = func(p int) {
			if sched[p] {
				return
			}
			var qs []int
			for q := range n.in[p] {
				if st := n.pg(q); st.dirty && !st.inFlight {
					qs = append(qs, q)
				}
			}
			sort.Ints(qs)
			for _, q := range qs {
				if !sched[q] {
					emit(q)
				}
			}
			sched[p] = true
			plan = append(plan, p)
		}
		for _, p := range chain {
			st := n.pages[p]
			if st.inFlight || st.oldest >= lsn {
				continue
			}
			if !sched[p] {
				emit(p)
			}
		}
		n.note("Plan(%d) -> %v", lsn, plan)
		return outcome{ok: true, plan: plan}
	}
	panic("unknown op " + op)
}
