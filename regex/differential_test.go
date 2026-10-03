package regex

import (
	"fmt"
	"math/rand"
	"testing"
)

// 本文件把 Engine 与一份按规则逐步写成的朴素模拟对照：
// 朴素执行器用递归实现（而非显式栈），预算/封禁状态机独立书写。

// naiveExec 为递归实现的朴素回溯执行器。
type naiveExec struct {
	prog  []inst
	in    []byte
	limit int64
	memo  bool
	seen  map[[2]int]bool
	steps int64
}

// run 调度 (pc,pos)：返回 (匹配终点, 是否匹配, 是否超限)。
func (x *naiveExec) run(pc, pos int) (int, bool, bool) {
	if x.memo {
		key := [2]int{pc, pos}
		if x.seen[key] {
			return 0, false, false // 已调度过：视为失败且不计步
		}
		x.seen[key] = true
	}
	if x.steps == x.limit {
		return 0, false, true
	}
	x.steps++
	ins := x.prog[pc]
	switch ins.op {
	case opChar:
		if pos < len(x.in) && x.in[pos] == ins.b {
			return x.run(pc+1, pos+1)
		}
		return 0, false, false
	case opAny:
		if pos < len(x.in) && x.in[pos] != '\n' {
			return x.run(pc+1, pos+1)
		}
		return 0, false, false
	case opClass:
		if pos < len(x.in) && ins.set[x.in[pos]] {
			return x.run(pc+1, pos+1)
		}
		return 0, false, false
	case opAssertStart:
		if pos == 0 {
			return x.run(pc+1, pos)
		}
		return 0, false, false
	case opAssertEnd:
		if pos == len(x.in) {
			return x.run(pc+1, pos)
		}
		return 0, false, false
	case opSplit:
		if end, ok, lim := x.run(ins.x, pos); ok || lim {
			return end, ok, lim
		}
		return x.run(ins.y, pos)
	case opJmp:
		return x.run(ins.x, pos)
	case opMatch:
		return pos, true, false
	}
	return 0, false, false
}

// naiveSearch 从起点 0..n 依次尝试。
func naiveSearch(prog []inst, input []byte, limit int64, memo bool) (matched, limited bool, start, end int, steps int64) {
	x := &naiveExec{prog: prog, in: input, limit: limit, memo: memo}
	if memo {
		x.seen = make(map[[2]int]bool)
	}
	for s := 0; s <= len(input); s++ {
		if e, ok, lim := x.run(0, s); ok {
			return true, false, s, e, x.steps
		} else if lim {
			return false, true, 0, 0, x.steps
		}
	}
	return false, false, 0, 0, x.steps
}

// naiveEngine 为按规则逐步写成的朴素预算/封禁模拟。
type naiveEngine struct {
	l, e, g, k, d int64
	maxNow        int64
	epoch         int64
	rem           int64
	pats          map[string]*naivePat
}

type naivePat struct {
	prog []inst
	memo bool
	c    int64
	b    int64
	u    int64
}

func newNaiveEngine(l, e, g, k, d int64) *naiveEngine {
	return &naiveEngine{l: l, e: e, g: g, k: k, d: d, rem: g, pats: make(map[string]*naivePat)}
}

func (n *naiveEngine) match(id string, input []byte, now int64) (MatchResult, error) {
	if len(id) == 0 || len(id) > MaxIDLen || len(input) > MaxInputLen || now < 0 || now > MaxNow {
		return MatchResult{}, ErrInvalidArgs
	}
	p, ok := n.pats[id]
	if !ok {
		return MatchResult{}, ErrNotRegistered
	}
	if now < n.maxNow {
		return MatchResult{}, ErrClockRegression
	}
	if now < p.u {
		return MatchResult{}, ErrBanned
	}
	epoch := now / n.e
	remP := n.rem
	if epoch != n.epoch {
		remP = n.g
	}
	if remP == 0 {
		return MatchResult{}, ErrGlobalBudgetExhausted
	}
	lambda := n.l
	local := true
	if remP < n.l {
		lambda = remP
		local = false
	}
	matched, limited, start, end, steps := naiveSearch(p.prog, input, lambda, p.memo)
	n.maxNow = now
	if epoch != n.epoch {
		n.epoch = epoch
		n.rem = n.g
	}
	n.rem -= steps
	out := MatchResult{Steps: steps}
	switch {
	case matched:
		out.Outcome = OutcomeMatch
		out.Start, out.End = start, end
		p.c = 0
	case !limited:
		out.Outcome = OutcomeNoMatch
		p.c = 0
	case local:
		out.Outcome = OutcomeLocalLimit
		p.c++
		if p.c >= n.k {
			p.b++
			mult := int64(8)
			if p.b < 4 {
				mult = int64(1) << (p.b - 1)
			}
			p.u = now + n.d*mult
			p.c = 0
		}
	default:
		out.Outcome = OutcomeGlobalLimit
	}
	return out, nil
}

// ---- 随机模式与输入生成 ----

func genPattern(r *rand.Rand) string {
	s, _ := genAlt(r, 0)
	return s
}

func genAlt(r *rand.Rand, depth int) (string, bool) {
	n := 1 + r.Intn(2)
	out := ""
	anyNullable := false
	for i := 0; i < n; i++ {
		if i > 0 {
			out += "|"
		}
		s, nu := genConcat(r, depth)
		out += s
		if nu {
			anyNullable = true
		}
	}
	return out, anyNullable
}

func genConcat(r *rand.Rand, depth int) (string, bool) {
	n := 1 + r.Intn(3)
	out := ""
	allNullable := true
	for i := 0; i < n; i++ {
		s, nu := genItem(r, depth)
		out += s
		if !nu {
			allNullable = false
		}
	}
	return out, allNullable
}

// genItem 生成一个原子及可选量词，返回文本与该项是否可空。
// 为避免可空重复与"断言后紧跟量词"的语法错误，可空原子不再加量词。
func genItem(r *rand.Rand, depth int) (string, bool) {
	var atom string
	atomNullable := false
	switch r.Intn(10) {
	case 0:
		atom = "."
	case 1:
		atom = []string{"[ab]", "[a-c]", "[^a]", "[^ab]", "[ac]", "[b-c]"}[r.Intn(6)]
	case 2:
		if r.Intn(2) == 0 {
			atom = "^"
		} else {
			atom = "$"
		}
		atomNullable = true
	case 3, 4:
		if depth < 2 {
			inner, nu := genAlt(r, depth+1)
			atom = "(" + inner + ")"
			atomNullable = nu
		} else {
			atom = string('a' + byte(r.Intn(3)))
		}
	default:
		if r.Intn(8) == 0 {
			atom = []string{`\*`, `\.`, `\\`, `\[`}[r.Intn(4)]
		} else {
			atom = string('a' + byte(r.Intn(3)))
		}
	}
	if atomNullable {
		return atom, true
	}
	lazy := ""
	if r.Intn(4) == 0 {
		lazy = "?"
	}
	switch r.Intn(5) {
	case 0:
		return atom, false
	case 1:
		return atom + "*" + lazy, true
	case 2:
		return atom + "+" + lazy, false
	case 3:
		return atom + "?" + lazy, true
	default:
		m := r.Intn(4)
		if r.Intn(2) == 0 {
			return fmt.Sprintf("%s{%d,}%s", atom, m, lazy), m == 0
		}
		n := m + r.Intn(3)
		if n == 0 {
			n = 1
		}
		return fmt.Sprintf("%s{%d,%d}%s", atom, m, n, lazy), m == 0
	}
}

func genInput(r *rand.Rand) string {
	alpha := []byte{'a', 'b', 'c', 'x', '\n'}
	n := r.Intn(11)
	out := make([]byte, n)
	for i := range out {
		out[i] = alpha[r.Intn(len(alpha))]
	}
	return string(out)
}

// TestDifferentialRandom 把 Engine 与朴素模拟对照 2000 组随机模式与输入序列，
// 日志打印输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	r := rand.New(rand.NewSource(20261003))
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		L := 1 + r.Int63n(30)
		E := 1 + r.Int63n(50)
		G := 1 + r.Int63n(60)
		K := 1 + r.Int63n(3)
		D := 1 + r.Int63n(40)
		P := int64(1000)
		eng, err := NewEngine(L, E, G, K, D, P)
		if err != nil {
			t.Fatal(err)
		}
		nav := newNaiveEngine(L, E, G, K, D)
		nPat := 1 + r.Intn(3)
		ids := make([]string, 0, nPat)
		for i := 0; i < nPat; i++ {
			id := fmt.Sprintf("p%d", i)
			pat := genPattern(r)
			memo := r.Intn(2) == 0
			if err := eng.Register(id, pat, memo); err != nil {
				t.Fatalf("trial %d: generated pattern %q rejected: %v", trial, pat, err)
			}
			ast, err := parse(pat)
			if err != nil {
				t.Fatalf("trial %d: parse %q: %v", trial, pat, err)
			}
			prog, err := compile(ast, P)
			if err != nil {
				t.Fatalf("trial %d: compile %q: %v", trial, pat, err)
			}
			nav.pats[id] = &naivePat{prog: prog, memo: memo}
			ids = append(ids, id)
			t.Logf("trial=%d register id=%q pattern=%q memo=%v | L=%d E=%d G=%d K=%d D=%d P=%d",
				trial, id, pat, memo, L, E, G, K, D, P)
		}
		now := int64(0)
		var sumSteps int64
		nOps := 3 + r.Intn(10)
		for op := 0; op < nOps; op++ {
			switch r.Intn(10) {
			case 0:
				now -= r.Int63n(5) // 可能时钟回退
			case 1:
				now += 500 // 纪元跳跃
			default:
				now += r.Int63n(20)
			}
			if now < 0 {
				now = 0
			}
			id := ids[r.Intn(len(ids))]
			if r.Intn(20) == 0 {
				id = "unknown"
			}
			input := genInput(r)
			// 判定依据（调用前的朴素视角）。
			basis := ""
			{
				ep := now / E
				remP := nav.rem
				if ep != nav.epoch {
					remP = G
				}
				lambda := L
				kind := "本地"
				if remP < L {
					lambda = remP
					kind = "全局"
				}
				basis = fmt.Sprintf("rem'=%d λ=%d(%s)", remP, lambda, kind)
			}
			gotR, gotErr := eng.Match(id, []byte(input), now)
			wantR, wantErr := nav.match(id, []byte(input), now)
			t.Logf("trial=%d op=%d Match(id=%q in=%q now=%d) -> outcome=%v interval=[%d,%d) steps=%d err=%v | 依据: %s rem=%d epoch=%d",
				trial, op, id, input, now, gotR.Outcome, gotR.Start, gotR.End, gotR.Steps, gotErr,
				basis, nav.rem, nav.epoch)
			if gotErr != wantErr {
				t.Fatalf("trial %d op %d: err %v != naive %v", trial, op, gotErr, wantErr)
			}
			if gotErr == nil {
				if gotR != wantR {
					t.Fatalf("trial %d op %d: %+v != naive %+v", trial, op, gotR, wantR)
				}
				sumSteps += gotR.Steps
				// 不变量：steps 不超过 λ。
				p := eng.pats[id]
				if bound := int64(len(p.prog)) * int64(len(input)+1); p.memo && gotR.Steps > bound {
					t.Fatalf("trial %d op %d: memo steps %d > prog×(n+1)=%d", trial, op, gotR.Steps, bound)
				}
			}
			// 不变量：rem 在 [0,G]，c 在 [0,K)。
			if eng.rem < 0 || eng.rem > G {
				t.Fatalf("trial %d op %d: rem=%d out of [0,%d]", trial, op, eng.rem, G)
			}
			for pid, p := range eng.pats {
				if p.c < 0 || p.c >= K {
					t.Fatalf("trial %d op %d: pattern %s c=%d out of [0,%d)", trial, op, pid, p.c, K)
				}
			}
			// 引擎状态与朴素模拟一致。
			if eng.rem != nav.rem || eng.epoch != nav.epoch || eng.maxNow != nav.maxNow {
				t.Fatalf("trial %d op %d: state rem=%d/%d epoch=%d/%d maxNow=%d/%d",
					trial, op, eng.rem, nav.rem, eng.epoch, nav.epoch, eng.maxNow, nav.maxNow)
			}
			for pid, p := range eng.pats {
				np := nav.pats[pid]
				if p.c != np.c || p.b != np.b || p.u != np.u {
					t.Fatalf("trial %d op %d: pattern %s c/b/u = %d/%d/%d != naive %d/%d/%d",
						trial, op, pid, p.c, p.b, p.u, np.c, np.b, np.u)
				}
			}
		}
		// 非导出计数器：实际调度总数等于被接受 Match 的 steps 之和。
		if eng.dispatched != sumSteps {
			t.Fatalf("trial %d: dispatched=%d != sum of steps=%d", trial, eng.dispatched, sumSteps)
		}
	}
}
