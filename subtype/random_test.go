package subtype

import (
	"math/rand"
	"testing"
)

// ---- 独立朴素模型：把递归展开到足够深度后比较 ----
//
// 理论依据：判定引擎计算的是单调算子 F 在有限对集合 P 上的最大不动点，
// 而 F 的 Kleene 链 F^0(⊤) ⊇ F^1(⊤) ⊇ ... 每严格一步至少移除一对，
// 故展开深度达到 |P| 后必稳定于最大不动点。
// 这里 |P| ≤ |V(s)| × |V(t)|（两侧静态闭包节点数之积），
// 因此 naiveSubtype 以 depth = |V(s)|×|V(t)| 展开即与引擎等价。
// 该模型直接按规则递归书写，与引擎的不动点实现相互独立。

type naiveKey struct {
	s, t  *Type
	depth int
}

func naiveSubtype(defs map[string]*Type, s, t *Type, depth int, memo map[naiveKey]bool) bool {
	if depth <= 0 {
		return true
	}
	k := naiveKey{s, t, depth}
	if v, ok := memo[k]; ok {
		return v
	}
	v := naiveStep(defs, s, t, depth, memo)
	memo[k] = v
	return v
}

func naiveStep(defs map[string]*Type, s, t *Type, depth int, memo map[naiveKey]bool) bool {
	if t.Kind == KindTop {
		return true
	}
	if s.Kind == KindBottom {
		return true
	}
	if s.Kind == KindRef && t.Kind == KindRef && s.Name == t.Name {
		return true
	}
	if s.Kind == KindRef {
		return naiveSubtype(defs, defs[s.Name], t, depth-1, memo)
	}
	if t.Kind == KindRef {
		return naiveSubtype(defs, s, defs[t.Name], depth-1, memo)
	}
	if s.Kind == KindUnion {
		for _, m := range s.Members {
			if !naiveSubtype(defs, m, t, depth-1, memo) {
				return false
			}
		}
		return true
	}
	if t.Kind == KindUnion {
		for _, m := range t.Members {
			if naiveSubtype(defs, s, m, depth-1, memo) {
				return true
			}
		}
		return false
	}
	switch s.Kind {
	case KindInt, KindFloat, KindString, KindBool:
		return primLE(s.Kind, t.Kind)
	case KindObject:
		if t.Kind != KindObject {
			return false
		}
		for _, pt := range t.Props {
			ps, found := findProp(s, pt.Name)
			if !found {
				if !pt.Optional {
					return false
				}
				continue
			}
			if !pt.Optional && ps.Optional {
				return false
			}
			if pt.ReadOnly {
				if !naiveSubtype(defs, ps.Type, pt.Type, depth-1, memo) {
					return false
				}
			} else {
				if ps.ReadOnly {
					return false
				}
				if !naiveSubtype(defs, ps.Type, pt.Type, depth-1, memo) {
					return false
				}
				if !naiveSubtype(defs, pt.Type, ps.Type, depth-1, memo) {
					return false
				}
			}
		}
		return true
	case KindFunc:
		if t.Kind != KindFunc || len(s.Params) > len(t.Params) {
			return false
		}
		for i, sp := range s.Params {
			if !naiveSubtype(defs, t.Params[i], sp, depth-1, memo) {
				return false
			}
		}
		return naiveSubtype(defs, s.Ret, t.Ret, depth-1, memo)
	}
	return false
}

// ---- 随机类型生成 ----

var propNamePool = []string{"a", "b", "c", "d"}

func randType(r *rand.Rand, names []string, depth int) *Type {
	n := 6
	if depth > 0 {
		n = 10
	}
	switch c := r.Intn(n); {
	case c == 0:
		return Int()
	case c == 1:
		return Float()
	case c == 2:
		if r.Intn(2) == 0 {
			return Str()
		}
		return Boolean()
	case c == 3:
		return Top()
	case c == 4:
		return Bottom()
	case c == 6: // 对象
		np := r.Intn(4)
		perm := r.Perm(len(propNamePool))
		var props []Prop
		for i := 0; i < np && i < len(perm); i++ {
			props = append(props, Prop{
				Name:     propNamePool[perm[i]],
				Type:     randType(r, names, depth-1),
				Optional: r.Intn(3) == 0,
				ReadOnly: r.Intn(3) == 0,
			})
		}
		return Obj(props...)
	case c == 7: // 函数
		params := make([]*Type, r.Intn(3))
		for i := range params {
			params[i] = randType(r, names, depth-1)
		}
		return Fn(params, randType(r, names, depth-1))
	case c == 8: // 联合
		ms := make([]*Type, r.Intn(4))
		for i := range ms {
			ms[i] = randType(r, names, depth-1)
		}
		return Union(ms...)
	default: // c == 5 或 9：引用（提高引用出现率）
		if len(names) > 0 {
			return Ref(names[r.Intn(len(names))])
		}
		return Int()
	}
}

// randDef 生成保证受保护的命名定义：顶层是对象/函数/基本类型，
// 所有自引用都经过构造子，不会产生无保护循环。
func randDef(r *rand.Rand, names []string) *Type {
	for {
		t := randType(r, names, 2)
		switch t.Kind {
		case KindObject, KindFunc, KindInt, KindFloat, KindString, KindBool:
			return t
		}
	}
}

// ---- 随机差分测试 ----

func TestRandomDifferential(t *testing.T) {
	const cases = 3000
	r := rand.New(rand.NewSource(20261007))
	t.Logf("种子 20261007，共 %d 个随机类型对；判定依据：与朴素展开模型（深度=|V(s)|×|V(t)|）对照", cases)
	namePool := []string{"A", "B", "C"}
	for i := 0; i < cases; i++ {
		reg := NewRegistry()
		names := namePool[:r.Intn(len(namePool)+1)]
		for _, n := range names {
			if err := reg.Register(n, randDef(r, names)); err != nil {
				t.Fatalf("用例 %d: 登记 %q 失败: %v", i, n, err)
			}
		}
		s := randType(r, names, 3)
		x := randType(r, names, 3)

		res, err := reg.Check(s, x)
		if err != nil {
			t.Fatalf("用例 %d: 意外错误: %v", i, err)
		}
		defs := reg.Snapshot()
		ln, _, _ := closure(defs, s)
		rn, _, _ := closure(defs, x)
		depth := len(ln)*len(rn) + 1
		naive := naiveSubtype(defs, s, x, depth, map[naiveKey]bool{})

		t.Logf("用例 %d: 输入 s=%s t=%s defs=%v；输出 engine=%v naive=%v；依据: 不动点 vs 深度-%d 展开；命名对 %d/%d",
			i, s, x, defs, res.Subtype, naive, depth, res.Stats.NamedPairs,
			res.Stats.LeftNames*res.Stats.RightNames)

		if naive != res.Subtype {
			t.Errorf("用例 %d 不一致: engine=%v naive=%v; s=%s t=%s defs=%v",
				i, res.Subtype, naive, s, x, defs)
		}
		st := res.Stats
		if st.NamedPairs > st.LeftNames*st.RightNames {
			t.Errorf("用例 %d 命名对越界: %d > %d*%d", i, st.NamedPairs, st.LeftNames, st.RightNames)
		}
		if st.PairsDiscovered > st.LeftNodes*st.RightNodes {
			t.Errorf("用例 %d 类型对越界: %d > %d*%d", i, st.PairsDiscovered, st.LeftNodes, st.RightNodes)
		}
	}
}
