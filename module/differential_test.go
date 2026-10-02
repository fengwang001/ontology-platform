package module

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// ---- 随机模块图与操作序列生成 ----

type randomSpec struct {
	name      string
	imports   []Import
	exports   []Export
	reexports []Reexport
	body      []Step
}

func (s randomSpec) String() string {
	return fmt.Sprintf("{名:%s 导入:%v 导出:%v 转出:%v 体:%v}",
		s.name, s.imports, s.exports, s.reexports, s.body)
}

var namePool = []string{"a", "b", "c", "d", "e", "f", "g", "h", "u", "v", "w", "x", "y", "z"}

func randomSource(r *rand.Rand, modNames []string, self int) string {
	if len(modNames) <= 1 || r.Intn(5) == 0 {
		return "X" + itoa(r.Intn(5)) // 未登记的名字
	}
	for {
		i := r.Intn(len(modNames))
		if i != self {
			return modNames[i]
		}
	}
}

func genSpecs(r *rand.Rand, n int) []randomSpec {
	modNames := make([]string, n)
	for i := range modNames {
		modNames[i] = "M" + itoa(i)
	}
	var specs []randomSpec
	for i := 0; i < n; i++ {
		pool := append([]string(nil), namePool...)
		r.Shuffle(len(pool), func(a, b int) { pool[a], pool[b] = pool[b], pool[a] })
		used := 0
		take := func() string { s := pool[used]; used++; return s }

		spec := randomSpec{name: modNames[i]}
		var letNames []string
		for k, cnt := 0, r.Intn(4); k < cnt; k++ {
			nm := take()
			kind := Func
			if r.Intn(2) == 0 {
				kind = Let
				letNames = append(letNames, nm)
			}
			spec.exports = append(spec.exports, Export{Name: nm, Kind: kind})
		}
		for k, cnt := 0, r.Intn(3); k < cnt; k++ {
			spec.reexports = append(spec.reexports,
				NamedReexport(take(), randomSource(r, modNames, i), pool[r.Intn(len(pool))]))
		}
		for k, cnt := 0, r.Intn(3); k < cnt; k++ {
			spec.reexports = append(spec.reexports, StarReexport(randomSource(r, modNames, i)))
		}
		for k, cnt := 0, r.Intn(4); k < cnt && used < len(pool); k++ {
			var ns []string
			for j, nc := 0, 1+r.Intn(3); j < nc && used < len(pool); j++ {
				ns = append(ns, take())
			}
			if len(ns) > 0 {
				spec.imports = append(spec.imports, imp(randomSource(r, modNames, i), ns...))
			}
		}
		for k, cnt := 0, r.Intn(5); k < cnt; k++ {
			switch r.Intn(3) {
			case 0:
				if len(letNames) > 0 {
					spec.body = append(spec.body, Init(letNames[r.Intn(len(letNames))]))
				}
			case 1:
				if len(spec.imports) > 0 && r.Intn(2) == 0 {
					im := spec.imports[r.Intn(len(spec.imports))]
					spec.body = append(spec.body, Read(im.Source, im.Names[r.Intn(len(im.Names))]))
				} else if len(spec.exports) > 0 {
					spec.body = append(spec.body, Read(spec.name, spec.exports[r.Intn(len(spec.exports))].Name))
				}
			case 2:
				spec.body = append(spec.body, Throw("e"+itoa(r.Intn(100))))
			}
		}
		specs = append(specs, spec)
	}
	return specs
}

type opKind int

const (
	opEval opKind = iota
	opStatus
	opOrder
)

type randOp struct {
	kind   opKind
	target string
}

func (o randOp) String() string {
	switch o.kind {
	case opEval:
		return "Evaluate(" + o.target + ")"
	case opStatus:
		return "Status(" + o.target + ")"
	}
	return "Order()"
}

func genOps(r *rand.Rand, n int) []randOp {
	var ops []randOp
	for i, cnt := 0, 3+r.Intn(8); i < cnt; i++ {
		target := "M" + itoa(r.Intn(n))
		if r.Intn(6) == 0 {
			target = "X" + itoa(r.Intn(5))
		}
		switch x := r.Intn(10); {
		case x < 7:
			ops = append(ops, randOp{opEval, target})
		case x < 9:
			ops = append(ops, randOp{opStatus, target})
		default:
			ops = append(ops, randOp{opOrder, ""})
		}
	}
	return ops
}

// ---- 对照 ----

func mustSameReject(t *testing.T, what string, a, b *Reject) {
	t.Helper()
	if (a == nil) != (b == nil) {
		t.Fatalf("%s: 拒绝不一致: %v vs %v", what, a, b)
	}
	if a == nil {
		return
	}
	if a.Reason != b.Reason || a.Module != b.Module || a.Source != b.Source || a.Name != b.Name {
		t.Fatalf("%s: 拒绝不一致: %+v vs %+v", what, a, b)
	}
}

func mustSameResult(t *testing.T, what string, a, b EvalResult) {
	t.Helper()
	if a.OK != b.OK || a.ErrValue != b.ErrValue || !reflect.DeepEqual(a.Appended, b.Appended) {
		t.Fatalf("%s: 结果不一致: %+v vs %+v", what, a, b)
	}
}

func naiveSnapshot(ns *naiveSession) snapshot {
	sn := snapshot{
		states: make(map[string]State),
		errs:   make(map[string]string),
		inited: make(map[string]map[string]bool),
		order:  ns.getOrder(),
	}
	for _, m := range ns.mods {
		sn.states[m.name] = m.state
		sn.errs[m.name] = m.errValue
		bits := make(map[string]bool)
		for k, v := range m.inited {
			bits[k] = v
		}
		sn.inited[m.name] = bits
	}
	return sn
}

func runDifferential(t *testing.T, iter int, seed int64) {
	r := rand.New(rand.NewSource(seed))
	n := 1 + r.Intn(8)
	specs := genSpecs(r, n)
	ops := genOps(r, n)
	t.Logf("迭代 %d seed=%d 输入: 模块=%v 操作=%v", iter, seed, specs, ops)

	real := NewSession()
	replay := NewSession()
	naive := newNaiveSession()

	for _, spec := range specs {
		rej1 := real.AddModule(spec.name, spec.imports, spec.exports, spec.reexports, spec.body)
		rej2 := replay.AddModule(spec.name, spec.imports, spec.exports, spec.reexports, spec.body)
		rej3 := naive.add(spec.name, spec.imports, spec.exports, spec.reexports, spec.body)
		mustSameReject(t, "AddModule "+spec.name, rej1, rej3)
		mustSameReject(t, "AddModule(重放) "+spec.name, rej1, rej2)
	}

	prev := capture(real)
	for _, op := range ops {
		switch op.kind {
		case opEval:
			res1, rej1 := real.Evaluate(op.target)
			res2, rej2 := replay.Evaluate(op.target)
			res3, rej3 := naive.evaluate(op.target)
			mustSameReject(t, op.String(), rej1, rej3)
			mustSameReject(t, op.String()+"(重放)", rej1, rej2)
			mustSameResult(t, op.String(), res1, res3)
			mustSameResult(t, op.String()+"(重放)", res1, res2)
			checkInvariants(t, real)
			checkCounters(t, real, prev, op.target, rej1)
		case opStatus:
			st1, rej1 := real.Status(op.target)
			st3, rej3 := naive.status(op.target)
			mustSameReject(t, op.String(), rej1, rej3)
			if rej1 == nil && !reflect.DeepEqual(st1, st3) {
				t.Fatalf("%s: 状态不一致: %+v vs %+v", op, st1, st3)
			}
		case opOrder:
			if got, want := real.Order(), naive.getOrder(); !reflect.DeepEqual(got, want) {
				t.Fatalf("Order: 不一致: %v vs %v", got, want)
			}
		}
		// 真实实现 vs 朴素模拟：全量状态一致（忽略非导出计数器列）。
		cur := capture(real)
		nsn := naiveSnapshot(naive)
		if !reflect.DeepEqual(cur.states, nsn.states) ||
			!reflect.DeepEqual(cur.errs, nsn.errs) ||
			!reflect.DeepEqual(cur.inited, nsn.inited) ||
			!reflect.DeepEqual(cur.order, nsn.order) {
			t.Fatalf("%s 后状态不一致:\n真实: %+v\n朴素: %+v", op, cur, nsn)
		}
		// 重放确定性：相同操作序列得到完全相同的状态。
		if !reflect.DeepEqual(cur, capture(replay)) {
			t.Fatalf("%s 后重放状态不一致", op)
		}
		prev = cur
	}
	t.Logf("迭代 %d 输出: 次序=%v 判定依据: 真实实现、重放会话与朴素模拟的拒绝、结果、次序、状态、错误值逐项一致",
		iter, real.Order())
}

// checkCounters 验证计数器与单调性不变量。
func checkCounters(t *testing.T, s *Session, prev snapshot, root string, rej *Reject) {
	t.Helper()
	// 解析集合：展开次数等于最终大小。
	if s.lastResolveExpansions != s.lastResolveSetSize {
		t.Fatalf("展开次数 %d != 解析集合大小 %d", s.lastResolveExpansions, s.lastResolveSetSize)
	}
	// let 位只会由假变真。
	for name, bits := range prev.inited {
		for k, v := range bits {
			if v && !s.modules[name].inited[k] {
				t.Fatalf("模块 %s 的位 %s 由真变假", name, k)
			}
		}
	}
	// 链接遍历访问数：已求值/出错根为 1，否则不超过新链接模块数加一。
	rootState := prev.states[root]
	if rootState == StateEvaluated || rootState == StateError {
		if s.lastLinkVisited != 1 {
			t.Fatalf("已终结根的 lastLinkVisited = %d，想要 1", s.lastLinkVisited)
		}
		return
	}
	if rej != nil {
		return // 被拒绝的调用不链接任何模块
	}
	newly := 0
	for name, st := range prev.states {
		if st == StateRegistered && s.modules[name].state != StateRegistered {
			newly++
		}
	}
	if s.lastLinkVisited > newly+1 {
		t.Fatalf("lastLinkVisited %d 超过新链接模块数 %d + 1", s.lastLinkVisited, newly)
	}
}

func TestDifferentialRandom(t *testing.T) {
	base := rand.New(rand.NewSource(1167))
	for i := 0; i < 2000; i++ {
		seed := base.Int63()
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runDifferential(t, i, seed)
		})
	}
}
