package defassign

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// This file contains an independent naive model of the semantics: it
// enumerates every possible execution path and simulates the assignment
// state step by step. Random programs are checked against it.

type mstate struct {
	asg  map[string]bool
	path []string
}

type mout struct {
	st   mstate
	ctrl int // 0 = fall through, 1 = break, 2 = return
}

type daKey struct {
	stmt int
	v    string
}

type model struct {
	reg        *Registry
	unassigned map[daKey]map[string]bool
	executed   map[int]bool
	used       map[int]bool
	steps      int
}

const modelStepLimit = 200000

var errModelOverflow = fmt.Errorf("model path explosion")

func withTok(s mstate, tok string) mstate {
	p := make([]string, len(s.path)+1)
	copy(p, s.path)
	p[len(s.path)] = tok
	return mstate{asg: s.asg, path: p}
}

func (m *model) enumList(ids []int, s mstate) []mout {
	outs := []mout{{st: s}}
	for _, id := range ids {
		var next []mout
		for _, o := range outs {
			if o.ctrl != 0 {
				next = append(next, o)
				continue
			}
			next = append(next, m.enumStmt(id, o.st)...)
			m.steps += len(next)
			if m.steps > modelStepLimit {
				panic(errModelOverflow)
			}
		}
		outs = next
	}
	return outs
}

func (m *model) enumStmt(id int, s mstate) []mout {
	n := m.reg.Node(id)
	switch n.Kind {
	case KindAssign:
		asg := make(map[string]bool, len(s.asg)+1)
		for k, v := range s.asg {
			asg[k] = v
		}
		asg[n.Var] = true
		return []mout{{st: mstate{asg: asg, path: s.path}}}
	case KindRead:
		if !s.asg[n.Var] {
			key := daKey{stmt: id, v: n.Var}
			set := m.unassigned[key]
			if set == nil {
				set = make(map[string]bool)
				m.unassigned[key] = set
			}
			set[renderPath(s.path)] = true
		}
		return []mout{{st: s}}
	case KindBlock:
		return m.enumList(n.Children, s)
	case KindIf:
		var outs []mout
		if n.Cond != CondFalse {
			outs = append(outs, m.enumList(n.Then, withTok(s, tokIfThen(id)))...)
		}
		if n.Cond != CondTrue {
			outs = append(outs, m.enumList(n.Else, withTok(s, tokIfElse(id)))...)
		}
		return outs
	case KindLoop:
		var outs []mout
		if !n.AtLeastOnce {
			outs = append(outs, mout{st: withTok(s, tokLoopZero(id))})
		}
		for _, o := range m.enumList(n.Body, withTok(s, tokLoopBody(id))) {
			if o.ctrl == 2 {
				outs = append(outs, o)
			} else {
				outs = append(outs, mout{st: o.st})
			}
		}
		return outs
	case KindBreak:
		return []mout{{st: withTok(s, tokBreak(id)), ctrl: 1}}
	case KindReturn:
		return []mout{{st: withTok(s, tokReturn(id)), ctrl: 2}}
	case KindTry:
		var outs []mout
		var cleanupIns []mstate
		for _, o := range m.enumList(n.Body, withTok(s, tokTryBody(id))) {
			if o.ctrl == 0 {
				cleanupIns = append(cleanupIns, o.st)
			} else {
				outs = append(outs, o)
			}
		}
		for i, h := range n.Handlers {
			// A throw may happen at any point of the body, so the handler
			// starts from the pre-try state.
			hn := m.reg.Node(h)
			for _, o := range m.enumList(hn.Body, withTok(s, tokTryHandler(id, i))) {
				if o.ctrl == 0 {
					cleanupIns = append(cleanupIns, o.st)
				} else {
					outs = append(outs, o)
				}
			}
		}
		cleanupIns = append(cleanupIns, withTok(s, tokTryUnhandled(id)))
		for _, ci := range cleanupIns {
			outs = append(outs, m.enumList(n.Cleanup, ci)...)
		}
		return outs
	}
	return nil
}

// enumEvents enumerates complete event sequences for the dead-assignment
// model. Loops are unrolled 0, 1 or 2 times: two iterations suffice because
// control flow never depends on variable values, so any read reachable via
// a back edge is witnessed by the second iteration.
func (m *model) enumEvents(ids []int, evs []ev) []evOut {
	outs := []evOut{{evs: evs}}
	for _, id := range ids {
		var next []evOut
		for _, o := range outs {
			if o.ctrl != 0 {
				next = append(next, o)
				continue
			}
			next = append(next, m.enumEventStmt(id, o.evs)...)
			m.steps += len(next)
			if m.steps > modelStepLimit {
				panic(errModelOverflow)
			}
		}
		outs = next
	}
	return outs
}

type ev struct {
	assign bool
	v      string
	stmt   int
}

type evOut struct {
	evs  []ev
	ctrl int
}

func (m *model) enumEventStmt(id int, evs []ev) []evOut {
	n := m.reg.Node(id)
	switch n.Kind {
	case KindAssign:
		return []evOut{{evs: append(append([]ev{}, evs...), ev{assign: true, v: n.Var, stmt: id})}}
	case KindRead:
		return []evOut{{evs: append(append([]ev{}, evs...), ev{v: n.Var, stmt: id})}}
	case KindBlock:
		return m.enumEvents(n.Children, evs)
	case KindIf:
		var outs []evOut
		if n.Cond != CondFalse {
			outs = append(outs, m.enumEvents(n.Then, evs)...)
		}
		if n.Cond != CondTrue {
			outs = append(outs, m.enumEvents(n.Else, evs)...)
		}
		return outs
	case KindLoop:
		var outs []evOut
		if !n.AtLeastOnce {
			outs = append(outs, evOut{evs: evs})
		}
		for _, o := range m.enumEvents(n.Body, evs) {
			if o.ctrl == 2 {
				outs = append(outs, o)
				continue
			}
			outs = append(outs, evOut{evs: o.evs})
			for _, o2 := range m.enumEvents(n.Body, o.evs) {
				if o2.ctrl == 2 {
					outs = append(outs, o2)
				} else {
					outs = append(outs, evOut{evs: o2.evs})
				}
			}
		}
		return outs
	case KindBreak:
		return []evOut{{evs: evs, ctrl: 1}}
	case KindReturn:
		return []evOut{{evs: evs, ctrl: 2}}
	case KindTry:
		var outs []evOut
		var cleanupIns [][]ev
		for _, o := range m.enumEvents(n.Body, evs) {
			if o.ctrl == 0 {
				cleanupIns = append(cleanupIns, o.evs)
			} else {
				outs = append(outs, o)
			}
		}
		for _, h := range n.Handlers {
			hn := m.reg.Node(h)
			for _, o := range m.enumEvents(hn.Body, evs) {
				if o.ctrl == 0 {
					cleanupIns = append(cleanupIns, o.evs)
				} else {
					outs = append(outs, o)
				}
			}
		}
		cleanupIns = append(cleanupIns, evs)
		for _, ci := range cleanupIns {
			outs = append(outs, m.enumEvents(n.Cleanup, ci)...)
		}
		return outs
	}
	return nil
}

// modelDiags computes the expected diagnostics by naive path enumeration.
// The second result is false when enumeration exploded and the program must
// be skipped.
func modelDiags(reg *Registry) (out []string, ok bool) {
	m := &model{
		reg:        reg,
		unassigned: make(map[daKey]map[string]bool),
		executed:   make(map[int]bool),
		used:       make(map[int]bool),
	}
	defer func() {
		if r := recover(); r != nil {
			if r == errModelOverflow {
				out, ok = nil, false
				return
			}
			panic(r)
		}
	}()
	root, _ := reg.Root()
	m.enumList([]int{root}, mstate{asg: make(map[string]bool)})
	for _, o := range m.enumEvents([]int{root}, nil) {
		_ = o.ctrl
		last := make(map[string]int)
		for _, e := range o.evs {
			if e.assign {
				m.executed[e.stmt] = true
				last[e.v] = e.stmt
			} else if l, okL := last[e.v]; okL {
				m.used[l] = true
			}
		}
	}
	var diags []Diagnostic
	for key, paths := range m.unassigned {
		var ps []string
		for p := range paths {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		diags = append(diags, Diagnostic{Pos: key.stmt, Kind: DiagUnassignedRead, Var: key.v, Paths: ps})
	}
	for stmt := range m.executed {
		if !m.used[stmt] {
			diags = append(diags, Diagnostic{Pos: stmt, Kind: DiagDeadAssign, Var: reg.Node(stmt).Var})
		}
	}
	sort.Slice(diags, func(i, j int) bool { return lessDiag(diags[i], diags[j]) })
	return diagStrings(diags), true
}

// Random program generator. Generated programs are always valid: variables
// are declared, breaks stay inside loops, handlers are attached, and the
// structure is a tree by construction.
type progGen struct {
	r    *rand.Rand
	reg  *Registry
	vars []string
}

func genProgram(seed int64) *Registry {
	g := &progGen{
		r:    rand.New(rand.NewSource(seed)),
		reg:  NewRegistry(),
		vars: []string{"v0", "v1", "v2", "v3"},
	}
	for _, v := range g.vars {
		g.reg.Declare(v)
	}
	g.reg.SetRoot(g.reg.Block(g.stmts(0, 0, 1+g.r.Intn(4))...))
	return g.reg
}

func (g *progGen) randVar() string { return g.vars[g.r.Intn(len(g.vars))] }

func (g *progGen) stmts(depth, loopDepth, n int) []int {
	ids := make([]int, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, g.stmt(depth, loopDepth))
	}
	return ids
}

func (g *progGen) stmt(depth, loopDepth int) int {
	for {
		switch c := g.r.Intn(10); {
		case c <= 2:
			return g.reg.Assign(g.randVar())
		case c <= 4:
			return g.reg.Read(g.randVar())
		case c == 5 && depth < 2:
			cond := Cond(g.r.Intn(3))
			then := g.stmts(depth+1, loopDepth, g.r.Intn(3))
			els := g.stmts(depth+1, loopDepth, g.r.Intn(3))
			return g.reg.If(cond, then, els)
		case c == 6 && depth < 2:
			body := g.stmts(depth+1, loopDepth+1, 1+g.r.Intn(3))
			return g.reg.Loop(body, g.r.Intn(2) == 0)
		case c == 7 && loopDepth > 0:
			return g.reg.Break()
		case c == 8 && depth < 2:
			body := g.stmts(depth+1, loopDepth, g.r.Intn(3))
			var hs []int
			for i := 0; i < g.r.Intn(3); i++ {
				hs = append(hs, g.reg.Handler(g.stmts(depth+1, loopDepth, g.r.Intn(3))...))
			}
			cleanup := g.stmts(depth+1, loopDepth, g.r.Intn(3))
			return g.reg.Try(body, hs, cleanup)
		case c == 9:
			return g.reg.Return()
		}
	}
}

// TestRandomProgramsMatchModel compares the checker against the independent
// naive model on randomly generated programs, logging each input, the
// actual output and the deciding paths.
func TestRandomProgramsMatchModel(t *testing.T) {
	skipped := 0
	for seed := int64(0); seed < 400; seed++ {
		reg := genProgram(seed)
		diags, ierr := Check(reg)
		if ierr != nil {
			t.Fatalf("seed %d: generated program rejected: %v", seed, ierr)
		}
		want, ok := modelDiags(reg)
		if !ok {
			skipped++
			continue
		}
		got := diagStrings(diags)
		t.Logf("seed %d\ninput:\n%sactual output:\n%s", seed, reg.String(), RenderDiags(diags))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d mismatch\nprogram:\n%s\nchecker: %q\nmodel:   %q",
				seed, reg.String(), got, want)
		}
	}
	t.Logf("skipped %d programs due to model path explosion", skipped)
}

// TestDeterministicOutput: repeated checks of one program produce
// byte-identical diagnostic lists.
func TestDeterministicOutput(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		reg := genProgram(seed)
		first, ierr := Check(reg)
		if ierr != nil {
			t.Fatalf("seed %d: %v", seed, ierr)
		}
		want := RenderDiags(first)
		for i := 0; i < 5; i++ {
			again, ierr := Check(reg)
			if ierr != nil {
				t.Fatalf("seed %d: %v", seed, ierr)
			}
			if got := RenderDiags(again); got != want {
				t.Fatalf("seed %d: non-deterministic output:\n%s\nvs\n%s", seed, want, got)
			}
		}
	}
}

// TestConcurrentChecks: many independent programs checked concurrently must
// not interfere with each other.
func TestConcurrentChecks(t *testing.T) {
	const programs = 16
	regs := make([]*Registry, programs)
	want := make([]string, programs)
	for i := 0; i < programs; i++ {
		regs[i] = genProgram(int64(1000 + i))
		diags, ierr := Check(regs[i])
		if ierr != nil {
			t.Fatalf("program %d: %v", i, ierr)
		}
		want[i] = RenderDiags(diags)
	}
	var wg sync.WaitGroup
	for round := 0; round < 8; round++ {
		for i := 0; i < programs; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				diags, ierr := Check(regs[i])
				if ierr != nil {
					t.Errorf("program %d: %v", i, ierr)
					return
				}
				if got := RenderDiags(diags); got != want[i] {
					t.Errorf("program %d: concurrent result differs:\n%s\nwant:\n%s", i, got, want[i])
				}
			}(i)
		}
	}
	wg.Wait()
}
