package aggview

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// naiveModel 是独立实现的朴素全量重算模型：不做任何增量维护，
// 每次校验都从底层实例、属性、链接的当前状态重新扫描求和。
type naiveModel struct {
	store *Store
	views map[ID]ViewDef
}

func newNaive(st *Store) *naiveModel {
	return &naiveModel{store: st, views: map[ID]ViewDef{}}
}

func (n *naiveModel) register(d ViewDef) { n.views[d.Name] = d }

func (n *naiveModel) recompute(viewName, group ID) Aggregate {
	d, ok := n.views[viewName]
	if !ok {
		return Aggregate{Sum: big.NewRat(0, 1)}
	}
	if gTyp, ok := n.store.ObjectType(group); !ok || gTyp != d.GroupType {
		return Aggregate{Sum: big.NewRat(0, 1)}
	}
	out := Aggregate{Sum: big.NewRat(0, 1)}
	for _, src := range d.Sources {
		// 朴素扫描：遍历该链接上当前指向 group 的全部实例。
		for _, member := range n.store.LinksTo(src.LinkType, group) {
			typ, ok := n.store.ObjectType(member)
			if !ok || typ != src.ObjectType {
				continue
			}
			v := n.store.GetProperty(member, src.PropType)
			if !v.Present {
				continue // 不存在：贡献 0 且不参与计数
			}
			k := len(n.store.LinksFrom(member, src.LinkType))
			if k == 0 {
				continue
			}
			var c *big.Rat
			if src.Policy == PolicyFull {
				c = new(big.Rat).Set(v.Rat)
			} else {
				c = new(big.Rat).Quo(v.Rat, big.NewRat(int64(k), 1))
			}
			out.Sum.Add(out.Sum, c)
			out.Count++
		}
	}
	return out
}

func (n *naiveModel) groupsOf(d ViewDef) []ID {
	var groups []ID
	// 朴素模型只校验引擎曾经见过的分组：用候选 id 全集探测存活分组。
	for id := range n.store.objs {
		if typ, ok := n.store.ObjectType(id); ok && typ == d.GroupType {
			groups = append(groups, id)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	return groups
}

func assertMatchNaive(t *testing.T, e *Engine, n *naiveModel) {
	t.Helper()
	for name, d := range n.views {
		// 同时校验存活分组与已删除分组（后者必须为零）。
		groups := n.groupsOf(d)
		groups = append(groups, "__deleted_probe__")
		for _, g := range groups {
			inc := e.Query(name, g)
			want := n.recompute(name, g)
			if inc.Sum.Cmp(want.Sum) != 0 || inc.Count != want.Count {
				t.Fatalf("divergence view=%s group=%s incremental=(%s,%d) naive=(%s,%d)",
					name, g, inc.Sum.RatString(), inc.Count,
					want.Sum.RatString(), want.Count)
			}
		}
	}
}

// 随机生成属性写入、链接增删与实例删除的交织序列，逐操作对拍。
func TestRandomDifferentialFullPolicy(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for iter := 0; iter < 20; iter++ {
		e, st, log := newTestEngine(t)
		registerPayroll(t, e, PolicyFull)
		nm := newNaive(st)
		nm.register(e.views[vPayroll].def)

		nGroups := 2 + rng.Intn(4)
		nEmps := 3 + rng.Intn(6)
		var groups []ID
		for i := 0; i < nGroups; i++ {
			g := ID("dept" + itoa(iter) + "_" + itoa(i))
			mustCreate(t, st, g, tDept)
			groups = append(groups, g)
		}
		var emps []ID
		for i := 0; i < nEmps; i++ {
			m := ID("emp" + itoa(iter) + "_" + itoa(i))
			mustCreate(t, st, m, tEmp)
			emps = append(emps, m)
		}

		for step := 0; step < 400; step++ {
			switch rng.Intn(5) {
			case 0, 1: // 属性写入（含不存在/零/正负分数）
				m := emps[rng.Intn(len(emps))]
				if !st.Exists(m) {
					break
				}
				v := randomValue(rng)
				if _, err := e.SetProperty(m, pSalary, v); err != nil {
					t.Fatal(err)
				}
			case 2: // 加链接
				m := emps[rng.Intn(len(emps))]
				g := groups[rng.Intn(len(groups))]
				if st.Exists(m) && st.Exists(g) {
					if _, err := e.AddLink(m, lBelong, g); err != nil {
						t.Fatal(err)
					}
				}
			case 3: // 删链接
				m := emps[rng.Intn(len(emps))]
				g := groups[rng.Intn(len(groups))]
				if st.Exists(m) {
					if _, err := e.RemoveLink(m, lBelong, g); err != nil {
						t.Fatal(err)
					}
				}
			case 4: // 删除实例（分组或成员），删除后重建以制造交织
				if rng.Intn(2) == 0 {
					g := groups[rng.Intn(len(groups))]
					if st.Exists(g) {
						if _, err := e.DeleteObject(g); err != nil {
							t.Fatal(err)
						}
						if err := st.CreateObject(g, tDept); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					m := emps[rng.Intn(len(emps))]
					if st.Exists(m) {
						if _, err := e.DeleteObject(m); err != nil {
							t.Fatal(err)
						}
						if err := st.CreateObject(m, tEmp); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			assertMatchNaive(t, e, nm)
		}
		// 日志需包含每次变更的输入、受影响分组与判定依据。
		if len(log.events) == 0 {
			t.Fatal("expected change events to be logged")
		}
	}
}

// EvenShare 策略的随机对拍（引入分数，验证 big.Rat 精确性）。
func TestRandomDifferentialEvenShare(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyEvenShare)
	nm := newNaive(st)
	nm.register(e.views[vPayroll].def)

	var groups []ID
	for i := 0; i < 4; i++ {
		g := ID("g" + itoa(i))
		mustCreate(t, st, g, tDept)
		groups = append(groups, g)
	}
	var emps []ID
	for i := 0; i < 6; i++ {
		m := ID("m" + itoa(i))
		mustCreate(t, st, m, tEmp)
		emps = append(emps, m)
	}
	for step := 0; step < 600; step++ {
		m := emps[rng.Intn(len(emps))]
		g := groups[rng.Intn(len(groups))]
		switch rng.Intn(3) {
		case 0:
			if _, err := e.SetProperty(m, pSalary, randomValue(rng)); err != nil {
				t.Fatal(err)
			}
		case 1:
			if _, err := e.AddLink(m, lBelong, g); err != nil {
				t.Fatal(err)
			}
		default:
			if _, err := e.RemoveLink(m, lBelong, g); err != nil {
				t.Fatal(err)
			}
		}
		assertMatchNaive(t, e, nm)
	}
}

func randomValue(rng *rand.Rand) Value {
	switch rng.Intn(6) {
	case 0:
		return AbsentValue()
	case 1:
		return FromInt(0) // 明确的零值
	default:
		num := int64(rng.Intn(21) - 10)
		if rng.Intn(2) == 0 {
			return FromInt(num)
		}
		den := int64(1 + rng.Intn(6))
		return FromRat(big.NewRat(num, den))
	}
}
