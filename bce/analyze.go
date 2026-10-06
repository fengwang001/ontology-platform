package bce

import "fmt"

// Stats makes the complexity claims verifiable: FactsScanned counts
// facts examined per proof (independent of program size), MeetOps
// counts entries compared at joins (independent of unrelated variables).
type Stats struct {
	FactsScanned int
	MeetOps      int
}

// Analyzer holds per-input analysis state. Each Analyze call builds its
// own Analyzer, so concurrent analyses of independent inputs never
// interfere.
type Analyzer struct {
	prog         *Program
	cfg          cfg
	loops        []*Loop
	loopByHeader map[string]*Loop
	rpo          []string
	stats        Stats
	ins          map[string]*FactSet
	outs         map[string]map[string]*FactSet
}

// Analyze validates the input and decides every bounds check. Invalid
// input yields only an error, never a partial report.
func Analyze(p *Program) (*Report, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	a := newAnalyzer(p)
	a.run()
	return a.report(), nil
}

func newAnalyzer(p *Program) *Analyzer {
	c := buildCFG(p)
	dom := dominators(p)
	loops := findLoops(p)
	finishLoops(p, loops, dom)
	a := &Analyzer{
		prog:         p,
		cfg:          c,
		loops:        loops,
		loopByHeader: map[string]*Loop{},
		ins:          map[string]*FactSet{},
		outs:         map[string]map[string]*FactSet{},
	}
	for _, lp := range loops {
		a.loopByHeader[lp.Header] = lp
	}
	a.rpo = a.computeRPO()
	return a
}

// computeRPO returns block names in reverse postorder from the entry.
func (a *Analyzer) computeRPO() []string {
	visited := map[string]bool{}
	var post []string
	var dfs func(n string)
	dfs = func(n string) {
		if visited[n] {
			return
		}
		visited[n] = true
		succs := append([]string(nil), a.cfg.succ[n]...)
		for i := 0; i < len(succs); i++ {
			for j := i + 1; j < len(succs); j++ {
				if succs[j] < succs[i] {
					succs[i], succs[j] = succs[j], succs[i]
				}
			}
		}
		for _, s := range succs {
			dfs(s)
		}
		post = append(post, n)
	}
	dfs(a.prog.Entry)
	for i, j := 0, len(post)-1; i < j; i, j = i+1, j-1 {
		post[i], post[j] = post[j], post[i]
	}
	return post
}

func (a *Analyzer) initialFacts() *FactSet {
	fs := newFactSet(true)
	for _, arr := range a.prog.Arrays {
		if l, ok := a.prog.Lengths[arr]; ok {
			fs.addFact(fact{kind: fkLen, src: "decl", cval: l, arr: arr})
		}
	}
	return fs
}

// inOf computes the input fact set of block b from predecessor edges.
func (a *Analyzer) inOf(b string) *FactSet {
	var acc *FactSet
	if b == a.prog.Entry {
		// The entry may also be a loop header: its input is the meet of
		// the initial facts and any back edges.
		acc = a.initialFacts()
	}
	for _, p := range sortedKeysOf(a.cfg.pred[b]) {
		edge := a.outs[p][b]
		if edge == nil || !edge.reachable {
			continue
		}
		if acc == nil {
			acc = edge
		} else {
			acc = meet(acc, edge, a.loopByHeader[b], &a.stats)
		}
	}
	if acc == nil {
		return newFactSet(false)
	}
	return a.strengthen(b, acc)
}

// strengthen adds induction facts at loop headers: for an induction
// variable v with start s and constant positive (negative) step,
// v >= s (v <= s) holds at the header on every iteration.
func (a *Analyzer) strengthen(header string, fs *FactSet) *FactSet {
	lp := a.loopByHeader[header]
	if lp == nil || lp.Preheader == "" || !fs.reachable {
		return fs
	}
	pre := a.outs[lp.Preheader][header]
	if pre == nil || !pre.reachable {
		return fs
	}
	cp := *fs
	fs = &cp
	for _, ind := range lp.Ind {
		s, ok := pre.constOf(ind.Var)
		if !ok {
			continue
		}
		op := ">="
		if ind.Delta < 0 {
			op = "<="
		}
		fs.addFact(fact{
			kind: fkCmp,
			src:  "induction@" + header,
			cmp:  Cmp{Left: ind.Var, Op: op, RConst: true, RVal: s},
		})
	}
	return fs
}

func sortedKeysOf(vs []string) []string {
	out := append([]string(nil), vs...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// transferBlock pushes a fact set through one block.
func transferBlock(in *FactSet, b *Block) map[string]*FactSet {
	cur := *in
	for i, instr := range b.Instrs {
		transferInstr(&cur, instr, fmt.Sprintf("%s#%d", b.Name, i))
	}
	return edgeSets(&cur, b.Terms[0], b.Name)
}

func outsEqual(a, b map[string]*FactSet) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !v.equal(w) {
			return false
		}
	}
	return true
}

// run iterates the dataflow to a fixpoint. Facts only shrink and
// tombstones only grow, so the greatest fixpoint is unique and the
// result is independent of iteration order.
func (a *Analyzer) run() {
	for changed := true; changed; {
		changed = false
		for _, name := range a.rpo {
			b := a.prog.Block(name)
			in := a.inOf(name)
			a.ins[name] = in
			out := transferBlock(in, b)
			if !outsEqual(a.outs[name], out) {
				a.outs[name] = out
				changed = true
			}
		}
	}
}

// report walks each block once more and decides every check.
func (a *Analyzer) report() *Report {
	r := &Report{Program: a.prog.Name, Stats: a.stats}
	for _, b := range a.prog.Blocks {
		cur := *a.ins[b.Name]
		for i, instr := range b.Instrs {
			if instr.Kind == OpCheck {
				removed, reasons, consulted := decideCheck(&cur, instr.Arr, instr.Idx, &r.Stats)
				d := Decision{
					ID:      instr.ID,
					Block:   b.Name,
					Arr:     instr.Arr,
					Idx:     instr.Idx,
					Removed: removed,
					Reasons: reasons,
					Facts:   sortedFacts(consulted),
				}
				if !cur.reachable {
					d.Facts = []string{"(path infeasible)"}
				}
				r.Decisions = append(r.Decisions, d)
				if removed {
					r.Removed++
				}
			}
			transferInstr(&cur, instr, fmt.Sprintf("%s#%d", b.Name, i))
		}
	}
	return r
}
