package bce

import (
	"fmt"
	"math/rand"
	"testing"
)

// execResult is the outcome of one concrete execution.
type execResult struct {
	trapped    bool     // a kept check failed and halted the program
	trapCheck  string   // the kept check that trapped, if any
	keptFailed []string // kept checks that failed before the trap (at most the trap one)
	removedHit string   // a removed check that failed: unsound, must never happen
	ok         bool     // false when the step cap is exceeded
}

// execute runs the program concretely. It is the independent naive model:
// no facts, no proofs, just the operational semantics. A failing kept
// check traps (halts) the program, so a later check can never be masked
// by an earlier removed one; a failing removed check is recorded as a
// soundness violation.
func execute(p *Program, inputs map[string]int64, removed map[string]bool, stepCap int) execResult {
	env := map[string]int64{}
	arrs := map[string]int64{}
	index := map[string]int{}
	for i, b := range p.Blocks {
		index[b.ID] = i
	}
	eval := func(o Opnd) int64 {
		if o.IsConst {
			return o.Const
		}
		return env[o.Var]
	}
	cur := index[p.Entry]
	steps := 0
	var res execResult
	res.ok = true
	for {
		blk := p.Blocks[cur]
		var next int = -1
		for _, ins := range blk.Instrs {
			steps++
			if steps > stepCap {
				res.ok = false
				return res
			}
			switch t := ins.(type) {
			case Const:
				env[t.Dst] = t.Val
			case Input:
				env[t.Dst] = inputs[t.Dst]
			case Bin:
				if t.Op == Add {
					env[t.Dst] = eval(t.A) + eval(t.B)
				} else {
					env[t.Dst] = eval(t.A) - eval(t.B)
				}
			case Copy:
				env[t.Dst] = env[t.Src]
			case NewArray:
				n := eval(t.Size)
				if n < 0 {
					n = -n
				}
				if n > 64 {
					n = 64
				}
				arrs[t.Dst] = n
			case ArrCopy:
				arrs[t.Dst] = arrs[t.Src]
			case LenOf:
				env[t.Dst] = arrs[t.Arr]
			case Check:
				idx := env[t.Idx]
				if idx < 0 || idx >= arrs[t.Arr] {
					if removed[t.ID] {
						res.removedHit = t.ID
						return res
					}
					res.trapped = true
					res.trapCheck = t.ID
					res.keptFailed = append(res.keptFailed, t.ID)
					return res
				}
			case Jmp:
				next = index[t.To]
			case Br:
				a, b := eval(t.A), eval(t.B)
				var cond bool
				switch t.Op {
				case LT:
					cond = a < b
				case LE:
					cond = a <= b
				case GT:
					cond = a > b
				case GE:
					cond = a >= b
				case EQ:
					cond = a == b
				case NE:
					cond = a != b
				}
				if cond {
					next = index[t.Then]
				} else {
					next = index[t.Else]
				}
			case Ret:
				return res
			}
		}
		if next < 0 {
			res.ok = false
			return res
		}
		cur = next
	}
}

// gen is a random program generator. It only emits valid programs: every
// variable is defined before use, every block is reachable from the entry
// through the linear segment chain, and loops are structured with a
// unique entry.
type gen struct {
	r       *rand.Rand
	blocks  []*Block
	n       int
	checkN  int
	loopN   int
	scalars []string
	arrays  []string
	inputs  []string
}

func (g *gen) future(k int) string { return fmt.Sprintf("b%d", g.n+k) }

func (g *gen) addBlock(id string, instrs ...Instr) {
	g.blocks = append(g.blocks, &Block{ID: id, Instrs: instrs})
	g.n++
}

func (g *gen) scalar() string { return g.scalars[g.r.Intn(len(g.scalars))] }
func (g *gen) array() string  { return g.arrays[g.r.Intn(len(g.arrays))] }

func (g *gen) check(instrs []Instr, idx string) []Instr {
	id := fmt.Sprintf("c%d", g.checkN)
	g.checkN++
	return append(instrs, Check{ID: id, Arr: g.array(), Idx: idx})
}

// randomScalarOps appends random scalar computations over defined vars.
func (g *gen) randomScalarOps(instrs []Instr, avoid string) []Instr {
	for k := 0; k < g.r.Intn(3); k++ {
		dst := g.scalar()
		if dst == avoid {
			continue
		}
		switch g.r.Intn(3) {
		case 0:
			instrs = append(instrs, Bin{Dst: dst, Op: Add, A: V(g.scalar()), B: C(int64(g.r.Intn(4)))})
		case 1:
			instrs = append(instrs, Bin{Dst: dst, Op: Sub, A: V(g.scalar()), B: C(int64(g.r.Intn(4)))})
		case 2:
			instrs = append(instrs, Copy{Dst: dst, Src: g.scalar()})
		}
	}
	return instrs
}

func genProgram(r *rand.Rand) (*Program, []string) {
	g := &gen{r: r}
	// Entry block: inputs, arrays, constants.
	var entry []Instr
	for k := 0; k < 1+r.Intn(3); k++ {
		v := fmt.Sprintf("x%d", k)
		entry = append(entry, Input{Dst: v})
		g.scalars = append(g.scalars, v)
		g.inputs = append(g.inputs, v)
	}
	for k := 0; k < 1+r.Intn(2); k++ {
		a := fmt.Sprintf("a%d", k)
		if r.Intn(2) == 0 {
			entry = append(entry, NewArray{Dst: a, Size: C(int64(1 + r.Intn(10)))})
		} else {
			entry = append(entry, NewArray{Dst: a, Size: V(g.inputs[r.Intn(len(g.inputs))])})
		}
		g.arrays = append(g.arrays, a)
	}
	for k := 0; k < 1+r.Intn(3); k++ {
		v := fmt.Sprintf("k%d", k)
		entry = append(entry, Const{Dst: v, Val: int64(r.Intn(7) - 2)})
		g.scalars = append(g.scalars, v)
	}
	entry = append(entry, Jmp{To: g.future(1)})
	g.addBlock(g.future(0), entry...)

	segments := 3 + r.Intn(6)
	for s := 0; s < segments; s++ {
		switch r.Intn(5) {
		case 0: // straight-line block with random ops and maybe a check
			var instrs []Instr
			instrs = g.randomScalarOps(instrs, "")
			switch r.Intn(4) {
			case 0: // constant index: often removable
				cv := fmt.Sprintf("j%d", g.checkN)
				instrs = append(instrs, Const{Dst: cv, Val: int64(r.Intn(6))})
				g.scalars = append(g.scalars, cv)
				instrs = g.check(instrs, cv)
			case 1: // a check followed by a duplicate the analysis can remove
				idx := g.scalar()
				arr := g.array()
				id0 := fmt.Sprintf("c%d", g.checkN)
				g.checkN++
				instrs = append(instrs, Check{ID: id0, Arr: arr, Idx: idx})
				id1 := fmt.Sprintf("c%d", g.checkN)
				g.checkN++
				instrs = append(instrs, Check{ID: id1, Arr: arr, Idx: idx})
			case 2:
				instrs = g.check(instrs, g.scalar())
			}
			instrs = append(instrs, Jmp{To: g.future(1)})
			g.addBlock(g.future(0), instrs...)
		case 1: // if/else diamond with path conditions
			headID, thenID, elseID := g.future(0), g.future(1), g.future(2)
			joinID := g.future(3)
			ops := []CmpOp{LT, LE, GT, GE, EQ, NE}
			op := ops[r.Intn(len(ops))]
			var condB Opnd
			if r.Intn(2) == 0 {
				condB = C(int64(r.Intn(8) - 2))
			} else {
				condB = V(g.scalar())
			}
			g.addBlock(headID, Br{A: V(g.scalar()), Op: op, B: condB, Then: thenID, Else: elseID})
			var thenInstrs []Instr
			if r.Intn(2) == 0 {
				thenInstrs = g.check(thenInstrs, g.scalar())
			}
			thenInstrs = append(thenInstrs, Jmp{To: joinID})
			g.addBlock(thenID, thenInstrs...)
			var elseInstrs []Instr
			if r.Intn(2) == 0 {
				elseInstrs = g.check(elseInstrs, g.scalar())
			}
			elseInstrs = append(elseInstrs, Jmp{To: joinID})
			g.addBlock(elseID, elseInstrs...)
			g.addBlock(joinID, Jmp{To: g.future(1)})
		case 2: // while loop with an induction variable
			preID, headID, bodyID, exitID := g.future(0), g.future(1), g.future(2), g.future(3)
			iv := fmt.Sprintf("i%d", g.loopN)
			g.loopN++
			g.scalars = append(g.scalars, iv)
			step := int64(1 + r.Intn(2))
			pre := []Instr{Const{Dst: iv, Val: int64(r.Intn(3))}}
			var bound Opnd
			if r.Intn(2) == 0 {
				bound = C(int64(r.Intn(13)))
			} else {
				nv := fmt.Sprintf("n%d", g.loopN)
				pre = append(pre, LenOf{Dst: nv, Arr: g.array()})
				g.scalars = append(g.scalars, nv)
				bound = V(nv)
				if r.Intn(2) == 0 { // sometimes probe the equality boundary
					off := fmt.Sprintf("m%d", g.loopN)
					delta := int64(r.Intn(3) - 1) // -1, 0, +1
					pre = append(pre, Bin{Dst: off, Op: Add, A: V(nv), B: C(delta)})
					g.scalars = append(g.scalars, off)
					bound = V(off)
				}
			}
			op := LT
			if r.Intn(2) == 0 {
				op = LE
			}
			pre = append(pre, Jmp{To: headID})
			g.addBlock(preID, pre...)
			g.addBlock(headID, Br{A: V(iv), Op: op, B: bound, Then: bodyID, Else: exitID})
			var body []Instr
			body = g.check(body, iv)
			body = g.randomScalarOps(body, iv)
			body = append(body,
				Bin{Dst: iv, Op: Add, A: V(iv), B: C(step)},
				Jmp{To: headID},
			)
			g.addBlock(bodyID, body...)
		case 3: // do-while loop: check precedes the exit guard
			preID, bodyID, exitID := g.future(0), g.future(1), g.future(2)
			iv := fmt.Sprintf("i%d", g.loopN)
			g.loopN++
			g.scalars = append(g.scalars, iv)
			step := int64(1 + r.Intn(2))
			pre := []Instr{Const{Dst: iv, Val: int64(r.Intn(3))}}
			var bound Opnd
			if r.Intn(2) == 0 {
				bound = C(int64(r.Intn(13)))
			} else {
				nv := fmt.Sprintf("n%d", g.loopN)
				pre = append(pre, LenOf{Dst: nv, Arr: g.array()})
				g.scalars = append(g.scalars, nv)
				bound = V(nv)
				if r.Intn(2) == 0 {
					off := fmt.Sprintf("m%d", g.loopN)
					delta := int64(r.Intn(3) - 1)
					pre = append(pre, Bin{Dst: off, Op: Add, A: V(nv), B: C(delta)})
					g.scalars = append(g.scalars, off)
					bound = V(off)
				}
			}
			op := LT
			if r.Intn(2) == 0 {
				op = LE
			}
			pre = append(pre, Jmp{To: bodyID})
			g.addBlock(preID, pre...)
			var body []Instr
			body = g.check(body, iv)
			body = append(body,
				Bin{Dst: iv, Op: Add, A: V(iv), B: C(step)},
				Br{A: V(iv), Op: op, B: bound, Then: bodyID, Else: exitID},
			)
			g.addBlock(bodyID, body...)
		case 4: // array reassignment, then a check
			var instrs []Instr
			a := g.array()
			if r.Intn(2) == 0 && len(g.arrays) > 1 {
				src := g.array()
				instrs = append(instrs, ArrCopy{Dst: a, Src: src})
			} else {
				instrs = append(instrs, NewArray{Dst: a, Size: V(g.inputs[r.Intn(len(g.inputs))])})
			}
			instrs = g.check(instrs, g.scalar())
			instrs = append(instrs, Jmp{To: g.future(1)})
			g.addBlock(g.future(0), instrs...)
		}
	}
	g.addBlock(g.future(0), Ret{})
	return &Program{Entry: "b0", Blocks: g.blocks}, g.inputs
}

// TestNaiveModelCrossCheck compares the analysis against the naive model:
// randomly generated programs are executed with random inputs, and every
// check the analysis removed must never fail in any execution. Kept
// checks are allowed to have never failed. Every input, the observed
// output, and the decision basis are logged.
func TestNaiveModelCrossCheck(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		r := rand.New(rand.NewSource(seed))
		p, inputVars := genProgram(r)
		rep, err := Analyze(p)
		if err != nil {
			t.Fatalf("seed %d: generated program rejected: %v", seed, err)
		}
		removed := map[string]bool{}
		for _, d := range rep.Decisions {
			if d.Removed {
				removed[d.CheckID] = true
			}
		}
		trapCounts := map[string]int{}
		for trial := 0; trial < 20; trial++ {
			in := map[string]int64{}
			for _, v := range inputVars {
				in[v] = int64(r.Intn(17) - 4)
			}
			res := execute(p, in, removed, 500000)
			if !res.ok {
				t.Fatalf("seed %d: execution exceeded step cap", seed)
			}
			t.Logf("seed=%d trial=%d inputs=%v trapped=%v trap-check=%s",
				seed, trial, in, res.trapped, res.trapCheck)
			if res.removedHit != "" {
				t.Fatalf("seed %d: removed check %s failed at runtime (inputs=%v)\n%s",
					seed, res.removedHit, in, rep)
			}
			if res.trapped {
				trapCounts[res.trapCheck]++
			}
		}
		t.Logf("seed=%d decisions (basis: evidence/causes per check):\n%s", seed, rep)
		t.Logf("seed=%d kept checks that trapped executions: %v", seed, trapCounts)
	}
}
