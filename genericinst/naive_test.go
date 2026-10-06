package genericinst

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveModel 是独立的朴素参考模型：不缓存、不去重，每次请求重新计算等价性。
// 它只模拟本测试需要的语义：登记定义、请求实例（无嵌套体）、更新定义与过期。
// 等价判定通过与正式实现完全相同的规范化键进行（这是"重新计算等价性"的直接表达）。
type naiveInstance struct {
	id     int
	def    string
	key    string
	stale  bool
	deps   map[int]struct{}
	depend map[int]struct{}
}

type naiveModel struct {
	defs      map[string]*Def
	all       []*naiveInstance
	hits      int
	creates   int
	nextID    int
	maxPerDef int
	maxTotal  int
}

func newNaiveModel(maxTotal, maxPerDef int) *naiveModel {
	return &naiveModel{
		defs:      map[string]*Def{},
		maxTotal:  maxTotal,
		maxPerDef: maxPerDef,
	}
}

func (m *naiveModel) register(d *Def) { m.defs[d.Name] = cloneDef(d) }

func (m *naiveModel) update(d *Def) {
	m.defs[d.Name] = cloneDef(d)
	roots := []*naiveInstance{}
	byID := map[int]*naiveInstance{}
	for _, in := range m.all {
		byID[in.id] = in
		if !in.stale && in.def == d.Name {
			in.stale = true
			roots = append(roots, in)
		}
	}
	seen := map[int]bool{}
	for len(roots) > 0 {
		cur := roots[0]
		roots = roots[1:]
		for pid := range cur.depend {
			p := byID[pid]
			if p != nil && !p.stale && !seen[p.id] {
				p.stale = true
				seen[p.id] = true
				roots = append(roots, p)
			}
		}
	}
}

func (m *naiveModel) request(defName string, args []Type, nested ...[]reqSpec) (id int, code ErrorCode) {
	d, ok := m.defs[defName]
	if !ok {
		return -1, ErrUndefined
	}
	norm, e := normalizeArgs(d, args)
	if e != nil {
		return -1, e.Code
	}
	if e := checkConstraints(d, norm); e != nil {
		return -1, e.Code
	}
	key := defName + "\x00" + argsKey(norm)
	// 与正式实现一致：总配额口径包含过期实例（清理前仍占用）；
	// 单定义配额只统计该定义的实例。
	totalCount := len(m.all)
	perDef := 0
	for _, in := range m.all {
		if in.def == defName {
			perDef++
		}
	}
	// 朴素模型每次都线性扫描全部实例来判定等价（不去重/不缓存索引）。
	for _, in := range m.all {
		if !in.stale && in.def == defName && in.key == key {
			m.hits++
			return in.id, 0
		}
	}
	if totalCount >= m.maxTotal || perDef >= m.maxPerDef {
		return -1, ErrQuota
	}
	in := &naiveInstance{id: m.nextID, def: defName, key: key,
		deps: map[int]struct{}{}, depend: map[int]struct{}{}}
	m.nextID++
	base := len(m.all) // 追加父实例之前的基线：失败时回滚到这里
	createsBase := m.creates
	hitsBase := m.hits
	m.all = append(m.all, in)
	m.creates++
	// 朴素地执行嵌套请求（重新线性判定等价），失败则整体撤销本次新建。
	if len(nested) > 0 {
		failed := false
		var failCode ErrorCode
		for _, spec := range nested[0] {
			cid, cc := m.request(spec.def, spec.args)
			if cc != 0 {
				failed = true
				failCode = cc
				break
			}
			in.deps[cid] = struct{}{}
			if child, ok := m.findByID(cid); ok {
				child.depend[in.id] = struct{}{}
			}
		}
		if failed {
			// 删除本次请求新建的全部实例，并解除它们与任何存活实例之间的边。
			newIDs := map[int]bool{}
			for _, x := range m.all[base:] {
				newIDs[x.id] = true
			}
			for _, x := range m.all[base:] {
				for cid := range x.deps {
					if child, ok := m.findByID(cid); ok && !newIDs[child.id] {
						delete(child.depend, x.id)
					}
				}
				for pid := range x.depend {
					if p, ok := m.findByID(pid); ok && !newIDs[p.id] {
						delete(p.deps, x.id)
					}
				}
			}
			m.all = m.all[:base]
			m.creates = createsBase
			m.hits = hitsBase
			return -1, failCode
		}
	}
	return in.id, 0
}

type reqSpec struct {
	def  string
	args []Type
}

func (m *naiveModel) findByID(id int) (*naiveInstance, bool) {
	for _, in := range m.all {
		if in.id == id {
			return in, true
		}
	}
	return nil, false
}

func (m *naiveModel) cleanup(id int) ErrorCode {
	in, ok := m.findByID(id)
	if !ok || !in.stale {
		return ErrDependency
	}
	for pid := range in.depend {
		if p, ok := m.findByID(pid); ok && !p.stale {
			return ErrDependency
		}
	}
	idx := -1
	for i, x := range m.all {
		if x.id == id {
			idx = i
		}
	}
	m.all = append(m.all[:idx], m.all[idx+1:]...)
	return 0
}

func (m *naiveModel) validKeys() []string {
	out := []string{}
	for _, in := range m.all {
		if !in.stale {
			out = append(out, in.key)
		}
	}
	sort.Strings(out)
	return out
}

// op 是随机序列中的一个操作。
type op struct {
	kind   int // 0=request 1=update
	def    string
	argIdx int
}

func TestDifferentialRandomSequences(t *testing.T) {
	const seed = 20261006
	rng := rand.New(rand.NewSource(seed))
	defNames := []string{"D1", "D2", "D3"}
	typeArgs := []Type{
		NewNominal("int"),
		NewNominal("string"),
		NewAlias("IntAlias", NewNominal("int")), // 与 int 等价
		NewStruct([]StructField{{Name: "x", Type: NewNominal("int")}}),
		NewStruct([]StructField{{Name: "x", Type: NewNominal("string")}}),
		// 字段次序不同 -> 与上一个不等价
		NewStruct([]StructField{
			{Name: "y", Type: NewNominal("string")},
			{Name: "x", Type: NewNominal("int")},
		}),
	}
	const maxTotal, maxPerDef = 12, 6
	const iterations = 400

	for iter := 0; iter < iterations; iter++ {
		tl := NewTestLogger()
		real := New(WithMaxInstances(maxTotal), WithMaxPerDef(maxPerDef), WithLogger(tl))
		naive := newNaiveModel(maxTotal, maxPerDef)
		for _, n := range defNames {
			d := &Def{Name: n, Params: []Param{{Name: "T"}}}
			real.RegisterDef(d)
			naive.register(d)
		}

		seqLen := 1 + rng.Intn(14)
		for step := 0; step < seqLen; step++ {
			def := defNames[rng.Intn(len(defNames))]
			arg := typeArgs[rng.Intn(len(typeArgs))]
			if rng.Intn(6) == 0 {
				// 更新定义（声明保持不变，只触发失效）。
				real.UpdateDef(&Def{Name: def, Params: []Param{{Name: "T"}}})
				naive.update(&Def{Name: def, Params: []Param{{Name: "T"}}})
				continue
			}
			// 约三分之一的请求带一层嵌套；深度上限 3 足以覆盖一层嵌套。
			var realBody Body
			var nestedSpecs []reqSpec
			nested := false
			if rng.Intn(3) == 0 {
				nested = true
				childDef := defNames[rng.Intn(len(defNames))]
				childArg := typeArgs[rng.Intn(len(typeArgs))]
				nestedSpecs = []reqSpec{{def: childDef, args: []Type{childArg}}}
				realBody = func(cd string, ca Type) Body {
					return func(s *Session) error {
						_, e2 := s.Request(cd, []Type{ca}, nil)
						return e2
					}
				}(childDef, childArg)
			}
			rid, re := real.Request("cu", def, []Type{arg}, realBody)
			var nid int
			var ncode ErrorCode
			if nested {
				nid, ncode = naive.request(def, []Type{arg}, nestedSpecs)
			} else {
				nid, ncode = naive.request(def, []Type{arg})
			}
			t.Logf("iter=%d step=%d def=%s arg=%q => real(id=%d,err=%v) naive(id=%d,code=%v)",
				iter, step, def, arg.CanonKey(), idOrMinus(rid), errCode(re), nid, ncode)
			if errCode(re) != ncode {
				t.Fatalf("rejection mismatch at iter=%d step=%d: real=%v naive=%v",
					iter, step, errCode(re), ncode)
			}
			// 偶发地尝试清理一个随机现存实例，两边的拒绝结果必须一致。
			if re == nil && rng.Intn(4) == 0 && len(real.instances) > 0 {
				ids := []int{}
				for id := range real.instances {
					ids = append(ids, id)
				}
				sort.Ints(ids)
				victim := ids[rng.Intn(len(ids))]
				if _, exists := naive.findByID(victim); !exists {
					// 该实例可能已被朴素模型侧清理过；跳过。
					continue
				}
				rcErr := real.Cleanup(victim)
				ncErr := naive.cleanup(victim)
				if errCode(rcErr) != ncErr {
					t.Fatalf("iter=%d cleanup mismatch id=%d real=%v naive=%v",
						iter, victim, errCode(rcErr), ncErr)
				}
				if rcErr == nil {
					t.Logf("iter=%d cleanup id=%d accepted on both", iter, victim)
				}
			}
		}

		// 比较最终有效实例多重集（按 def+规范化键）：
		// 过期实例与更新后新建的同键有效实例可能并存，故按出现次数比较。
		realValid := map[string]int{}
		for _, in := range real.instances {
			if in.stale == nil {
				realValid[in.Def+"\x00"+argsKey(in.Args)]++
			}
		}
		naiveValid := map[string]int{}
		for _, in := range naive.all {
			if !in.stale {
				naiveValid[in.key]++
			}
		}
		if !equalIntMaps(realValid, naiveValid) {
			realStale := map[string]int{}
			for _, in := range real.instances {
				if in.stale != nil {
					realStale[in.Def+"\x00"+argsKey(in.Args)]++
				}
			}
			naiveStale := map[string]int{}
			for _, in := range naive.all {
				if in.stale {
					naiveStale[in.key]++
				}
			}
			t.Fatalf("iter=%d valid-set mismatch:\nvalid real=%v\nvalid naive=%v\nstale real=%v\nstale naive=%v\nLOG:\n%s",
				iter, realValid, naiveValid, realStale, naiveStale, strings.Join(tl.Lines, "\n"))
		}
		// 累计命中次数必须一致。
		rs := real.SnapshotView()
		if rs.TotalHits != naive.hits {
			t.Fatalf("iter=%d hits mismatch: real=%d naive=%d", iter, rs.TotalHits, naive.hits)
		}
		if rs.TotalCreates != naive.creates {
			t.Fatalf("iter=%d creates mismatch: real=%d naive=%d", iter, rs.TotalCreates, naive.creates)
		}
	}
}

func idOrMinus(in *Instance) int {
	if in == nil {
		return -1
	}
	return in.ID
}

func equalIntMaps(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
