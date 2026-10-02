package matcher

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// ---------- 朴素模拟：枚举有限值逐个判定 ----------

type value struct {
	ctor *ctorInfo
	args []*value
}

func valueString(v *value) string {
	if len(v.args) == 0 {
		return v.ctor.name
	}
	parts := make([]string, len(v.args))
	for i, a := range v.args {
		parts[i] = valueString(a)
	}
	return v.ctor.name + "(" + strings.Join(parts, ", ") + ")"
}

type enumerator struct {
	memo     map[*typeInfo]map[int][]*value
	count    int
	cap      int
	overflow bool
}

// enum 返回类型 t 中构造子嵌套深度不超过 d 的全部有限值。
func (en *enumerator) enum(t *typeInfo, d int) []*value {
	if d <= 0 || en.overflow {
		return nil
	}
	if m, ok := en.memo[t]; ok {
		if vs, ok := m[d]; ok {
			return vs
		}
	}
	var out []*value
	for _, c := range t.ctors {
		combos := [][]*value{{}}
		for _, f := range c.fields {
			sub := en.enum(f, d-1)
			var next [][]*value
			for _, prefix := range combos {
				for _, s := range sub {
					row := make([]*value, 0, len(prefix)+1)
					row = append(row, prefix...)
					row = append(row, s)
					next = append(next, row)
				}
			}
			combos = next
		}
		for _, cb := range combos {
			out = append(out, &value{ctor: c, args: cb})
		}
		en.count += len(combos)
		if en.count > en.cap {
			en.overflow = true
			return nil
		}
	}
	if en.memo[t] == nil {
		en.memo[t] = make(map[int][]*value)
	}
	en.memo[t][d] = out
	return out
}

func matchPat(p *Pat, v *value) bool {
	switch p.Kind {
	case WildPat:
		return true
	case OrPat:
		for _, a := range p.Alts {
			if matchPat(a, v) {
				return true
			}
		}
		return false
	default:
		if p.Name != v.ctor.name || len(p.Args) != len(v.args) {
			return false
		}
		for i, a := range p.Args {
			if !matchPat(a, v.args[i]) {
				return false
			}
		}
		return true
	}
}

func patString(p *Pat) string {
	switch p.Kind {
	case WildPat:
		return "_"
	case OrPat:
		parts := make([]string, len(p.Alts))
		for i, a := range p.Alts {
			parts[i] = patString(a)
		}
		return "Or(" + strings.Join(parts, ", ") + ")"
	default:
		if len(p.Args) == 0 {
			return p.Name
		}
		parts := make([]string, len(p.Args))
		for i, a := range p.Args {
			parts[i] = patString(a)
		}
		return p.Name + "(" + strings.Join(parts, ", ") + ")"
	}
}

func topBranches(p *Pat) []*Pat {
	if p.Kind == OrPat {
		return p.Alts
	}
	return []*Pat{p}
}

// matchMask 返回每个枚举值是否被模式 p 匹配。
func matchMask(p *Pat, vals []*value) []bool {
	m := make([]bool, len(vals))
	for i, v := range vals {
		m[i] = matchPat(p, v)
	}
	return m
}

// ---------- 反例文本解析（随机生成的名字均为字母数字） ----------

type ceParser struct {
	s string
	i int
}

func (p *ceParser) parsePat() *Pat {
	if p.i < len(p.s) && p.s[p.i] == '_' {
		p.i++
		return W()
	}
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] == '_' || p.s[p.i] >= '0' && p.s[p.i] <= '9' ||
		p.s[p.i] >= 'A' && p.s[p.i] <= 'Z' || p.s[p.i] >= 'a' && p.s[p.i] <= 'z') {
		p.i++
	}
	name := p.s[start:p.i]
	pat := C(name)
	if p.i < len(p.s) && p.s[p.i] == '(' {
		p.i++
		for {
			pat.Args = append(pat.Args, p.parsePat())
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i += 2 // ", "
				continue
			}
			break
		}
		p.i++ // ')'
	}
	return pat
}

func parseCE(s string) *Pat {
	p := &ceParser{s: s}
	pat := p.parsePat()
	if p.i != len(s) {
		panic("反例文本解析失败: " + s)
	}
	return pat
}

// ---------- 随机生成 ----------

func genTypes(rng *rand.Rand, e *Engine) ([]*typeInfo, bool) {
	nTypes := 1 + rng.Intn(3)
	var types []*typeInfo
	ctorSeq := 0
	for i := 0; i < nTypes; i++ {
		name := fmt.Sprintf("T%d", i)
		nCtors := 1 + rng.Intn(3)
		ctors := make([]Ctor, 0, nCtors)
		// 第一个构造子无字段，保证类型有有限值且最小深度为 1。
		ctors = append(ctors, Ctor{Name: fmt.Sprintf("C%d", ctorSeq)})
		ctorSeq++
		for j := 1; j < nCtors; j++ {
			nFields := rng.Intn(3) // 0..2
			fields := make([]string, 0, nFields)
			for k := 0; k < nFields; k++ {
				if rng.Intn(100) < 30 || len(types) == 0 {
					fields = append(fields, name) // 自引用
				} else {
					fields = append(fields, types[rng.Intn(len(types))].name)
				}
			}
			// 二分支自引用会导致值集随深度双指数爆炸，仅小概率允许。
			if rng.Intn(100) >= 12 {
				selfSeen := false
				kept := fields[:0]
				for _, f := range fields {
					if f == name {
						if selfSeen {
							continue
						}
						selfSeen = true
					}
					kept = append(kept, f)
				}
				fields = kept
			}
			ctors = append(ctors, Ctor{Name: fmt.Sprintf("C%d", ctorSeq), Fields: fields})
			ctorSeq++
		}
		if err := e.DefineType(name, ctors); err != nil {
			return nil, false
		}
		types = append(types, e.types[name])
	}
	return types, true
}

func genPat(rng *rand.Rand, t *typeInfo, fuel int) *Pat {
	roll := rng.Intn(100)
	if fuel <= 0 || roll < 30 {
		return W()
	}
	if roll < 45 {
		n := 1 + rng.Intn(3)
		alts := make([]*Pat, 0, n)
		for i := 0; i < n; i++ {
			alts = append(alts, genPat(rng, t, fuel-1))
		}
		return Or(alts...)
	}
	c := t.ctors[rng.Intn(len(t.ctors))]
	args := make([]*Pat, 0, len(c.fields))
	for _, f := range c.fields {
		args = append(args, genPat(rng, f, fuel-1))
	}
	return C(c.name, args...)
}

const (
	fuzzGroups  = 2000
	fuzzEnumCap = 2000
)

type fuzzOp struct {
	isCheck bool
	sessIdx int
	pat     *Pat
	guarded bool
}

// 与朴素模拟对照：随机类型与臂序列，逐条比对冗余判定、穷尽性与反例。
func TestFuzzAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	skipped := 0
	for g := 0; g < fuzzGroups; g++ {
		if !runFuzzGroup(t, rng, g) {
			skipped++
		}
	}
	t.Logf("共 %d 组, 因枚举值集过大跳过 %d 组", fuzzGroups, skipped)
	if skipped > fuzzGroups/10 {
		t.Fatalf("跳过组数过多: %d/%d", skipped, fuzzGroups)
	}
}

func runFuzzGroup(t *testing.T, rng *rand.Rand, g int) bool {
	t.Helper()
	e := NewEngine()
	types, ok := genTypes(rng, e)
	if !ok {
		t.Fatalf("组 %d: 类型生成失败", g)
	}
	nSessions := 1 + rng.Intn(2)
	sessTypes := make([]*typeInfo, nSessions)
	for i := 0; i < nSessions; i++ {
		sessTypes[i] = types[rng.Intn(len(types))]
	}

	// 先生成全部操作，确定本组模式最大深度。
	nOps := 6 + rng.Intn(9)
	ops := make([]fuzzOp, 0, nOps)
	maxDepth := 1
	for op := 0; op < nOps; op++ {
		o := fuzzOp{sessIdx: rng.Intn(nSessions)}
		if rng.Intn(100) < 25 {
			o.isCheck = true
		} else {
			o.pat = genPat(rng, sessTypes[o.sessIdx], 3)
			o.guarded = rng.Intn(100) < 30
			d, err := shapeDepth(o.pat)
			if err != nil {
				t.Fatalf("组 %d: 生成的模式不合法: %v", g, err)
			}
			if d > maxDepth {
				maxDepth = d
			}
		}
		ops = append(ops, o)
	}

	// 枚举每个类型的有限值（深度上限 = 模式最大深度 + 2）。
	vals := make(map[*typeInfo][]*value)
	for _, ti := range types {
		en := &enumerator{memo: make(map[*typeInfo]map[int][]*value), cap: fuzzEnumCap}
		vs := en.enum(ti, maxDepth+2)
		if en.overflow {
			return false // 值集过大，跳过本组
		}
		vals[ti] = vs
	}
	t.Logf("组 %d: 类型数 %d, 模式最大深度 %d, 枚举深度 %d", g, len(types), maxDepth, maxDepth+2)
	for _, ti := range types {
		t.Logf("  类型 %s: 构造子 %d 个, 枚举值 %d 个", ti.name, len(ti.ctors), len(vals[ti]))
	}

	type sessState struct {
		id      int
		typ     *typeInfo
		armCnt  int
		covered []bool // 每个枚举值是否已被不带守卫臂覆盖
	}
	var sessions []*sessState
	for i := 0; i < nSessions; i++ {
		id, err := e.NewSession(sessTypes[i].name)
		if err != nil {
			t.Fatalf("组 %d: NewSession 失败: %v", g, err)
		}
		sessions = append(sessions, &sessState{
			id:      id,
			typ:     sessTypes[i],
			covered: make([]bool, len(vals[sessTypes[i]])),
		})
	}

	for _, o := range ops {
		ss := sessions[o.sessIdx]
		svals := vals[ss.typ]
		if o.isCheck {
			// Check 对照。
			exh, ce, err := e.Check(ss.id)
			if err != nil {
				t.Fatalf("组 %d: Check 失败: %v", g, err)
			}
			nExh := true
			var witness *value
			for i, v := range svals {
				if !ss.covered[i] {
					nExh = false
					witness = v
					break
				}
			}
			if exh != nExh {
				t.Fatalf("组 %d 会话 %d: 穷尽性不一致 algo=%v naive=%v", g, ss.id, exh, nExh)
			}
			if exh {
				if ce != "" {
					t.Fatalf("组 %d 会话 %d: 穷尽但反例非空 %q", g, ss.id, ce)
				}
				t.Logf("  Check 会话 %d: 穷尽（朴素一致）", ss.id)
				continue
			}
			if ce == "" {
				t.Fatalf("组 %d 会话 %d: 不穷尽但无反例，朴素反例 %s", g, ss.id, valueString(witness))
			}
			// 反例必须不被任何不带守卫臂匹配，且至少匹配一个枚举值。
			cePat := parseCE(ce)
			matched := 0
			for i, v := range svals {
				if !matchPat(cePat, v) {
					continue
				}
				matched++
				if ss.covered[i] {
					t.Fatalf("组 %d 会话 %d: 反例 %s 的值 %s 已被不带守卫臂覆盖",
						g, ss.id, ce, valueString(v))
				}
			}
			if matched == 0 {
				t.Fatalf("组 %d 会话 %d: 反例 %s 不匹配任何枚举值", g, ss.id, ce)
			}
			t.Logf("  Check 会话 %d: 不穷尽, 反例 %s（覆盖 %d 个未覆盖枚举值, 朴素见证 %s）",
				ss.id, ce, matched, valueString(witness))
			continue
		}
		// AddArm 对照。
		pat := o.pat
		guarded := o.guarded
		idx, branches, arm, err := e.AddArm(ss.id, pat, guarded)
		if err != nil {
			t.Fatalf("组 %d 会话 %d: AddArm(%s) 失败: %v", g, ss.id, patString(pat), err)
		}
		// 朴素判定：逐顶层分支，值被此前不带守卫臂或本臂更前分支覆盖才算冗余。
		tops := topBranches(pat)
		nBranches := make([]bool, len(tops))
		reasons := make([]string, len(tops))
		nArm := true
		earlier := make([]bool, len(svals))
		for bi, b := range tops {
			mb := matchMask(b, svals)
			red := true
			for i := range svals {
				if mb[i] && !ss.covered[i] && !earlier[i] {
					red = false
					reasons[bi] = "首个未覆盖值 " + valueString(svals[i])
					break
				}
			}
			if red {
				reasons[bi] = "全部匹配值均被此前不带守卫臂或本臂更前分支覆盖"
			}
			for i := range svals {
				earlier[i] = earlier[i] || mb[i]
			}
			nBranches[bi] = red
			if !red {
				nArm = false
			}
		}
		if idx != ss.armCnt+1 || !boolsEq(branches, nBranches) || arm != nArm {
			t.Fatalf("组 %d 会话 %d: 臂 %s (guarded=%v) 判定不一致\n  algo: idx=%d %v arm=%v\n  naive: idx=%d %v arm=%v",
				g, ss.id, patString(pat), guarded, idx, branches, arm, ss.armCnt+1, nBranches, nArm)
		}
		t.Logf("  会话 %d 臂 %d: %s guarded=%v -> 分支冗余 %v, 整臂 %v", ss.id, idx, patString(pat), guarded, branches, arm)
		for i, r := range reasons {
			t.Logf("    分支 %d 判定依据: %s", i, r)
		}
		ss.armCnt++
		if !guarded {
			for i := range svals {
				ss.covered[i] = ss.covered[i] || earlier[i]
			}
		}
	}
	return true
}
