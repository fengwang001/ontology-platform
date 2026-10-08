package ontology

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"
)

// 本文件是独立实现的朴素穷举角色路径参照模型：
// 不使用邻接索引、不做记忆化，直接穷举全部满足角色序列的路径，
// 再从路径集合上推导消歧与最终结果，用于与查询引擎对照。

type naivePath struct {
	objs  []ObjectID
	types []LinkTypeName
	cost  int64
}

// naiveEnumerate 穷举所有从 Start 出发、逐步匹配角色序列、
// 且只经过调用者有遍历权限链接类型的完整路径。
func naiveEnumerate(s *Store, q Query) []naivePath {
	var paths []naivePath
	var rec func(obj ObjectID, i int, objs []ObjectID, types []LinkTypeName, cost int64)
	rec = func(obj ObjectID, i int, objs []ObjectID, types []LinkTypeName, cost int64) {
		if i == len(q.Roles) {
			if obj == q.End {
				paths = append(paths, naivePath{
					objs:  slices.Clone(objs),
					types: slices.Clone(types),
					cost:  cost,
				})
			}
			return
		}
		role := q.Roles[i]
		for k := range s.links { // 全量扫描链接，不依赖任何索引
			if k.from != obj {
				continue
			}
			lt := s.linkTypes[k.typ]
			if _, ok := lt.Roles[role]; !ok {
				continue
			}
			if _, ok := s.traversal[q.Caller][k.typ]; !ok {
				continue
			}
			rec(k.to, i+1, append(objs, k.to), append(types, k.typ), cost+lt.Cost)
		}
	}
	rec(q.Start, 0, []ObjectID{q.Start}, nil, 0)
	return paths
}

type naiveState struct {
	i   int
	obj ObjectID
}

func naiveQuery(s *Store, q Query) Result {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(q.Roles) == 0 {
		return Result{Status: StatusInvalidArgument}
	}
	for _, r := range q.Roles {
		if s.roleRefs[r] <= 0 {
			return Result{Status: StatusInvalidArgument}
		}
	}
	if _, ok := s.existence[q.Caller][q.Start]; !ok {
		return Result{Status: StatusPermissionDenied}
	}
	if _, ok := s.existence[q.Caller][q.End]; !ok {
		return Result{Status: StatusPermissionDenied}
	}

	paths := naiveEnumerate(s, q)

	// 每个出现在某条完整路径上的状态，其可行候选类型集合。
	cand := make(map[naiveState]map[LinkTypeName]int)
	for _, p := range paths {
		for i := range p.types {
			key := naiveState{i: i, obj: p.objs[i]}
			if cand[key] == nil {
				cand[key] = make(map[LinkTypeName]int)
			}
			cand[key][p.types[i]] = s.linkTypes[p.types[i]].Roles[q.Roles[i]]
		}
	}

	chosen := make(map[naiveState]LinkTypeName)
	amb := make(map[naiveState]AmbiguousStep)
	for key, types := range cand {
		minPri := int(^uint(0) >> 1)
		for _, pri := range types {
			if pri < minPri {
				minPri = pri
			}
		}
		var top []LinkTypeName
		for typ, pri := range types {
			if pri == minPri {
				top = append(top, typ)
			}
		}
		if len(top) > 1 {
			sort.Slice(top, func(a, b int) bool { return top[a] < top[b] })
			amb[key] = AmbiguousStep{
				Index: key.i, At: key.obj, Role: q.Roles[key.i],
				Candidates: top, Priority: minPri,
			}
		} else {
			chosen[key] = top[0]
		}
	}

	var clean []naivePath
	for _, p := range paths {
		ok := true
		for i := range p.types {
			key := naiveState{i: i, obj: p.objs[i]}
			if _, isAmb := amb[key]; isAmb {
				ok = false
				break
			}
			if chosen[key] != p.types[i] {
				ok = false
				break
			}
		}
		if ok {
			clean = append(clean, p)
		}
	}

	res := Result{Ambiguous: naiveAmbList(amb)}
	if len(clean) > 0 {
		best := clean[0]
		for _, p := range clean[1:] {
			if p.cost < best.cost ||
				(p.cost == best.cost && slices.Compare(p.objs, best.objs) < 0) {
				best = p
			}
		}
		res.Status = StatusOK
		res.TotalCost = best.cost
		for i, typ := range best.types {
			lt := s.linkTypes[typ]
			res.Steps = append(res.Steps, Step{
				Index: i, From: best.objs[i], To: best.objs[i+1],
				LinkType: typ, Role: q.Roles[i], Cost: lt.Cost,
			})
		}
		return res
	}
	if len(amb) > 0 {
		res.Status = StatusAmbiguous
		return res
	}
	res.Status = StatusUnreachable
	return res
}

func naiveAmbList(amb map[naiveState]AmbiguousStep) []AmbiguousStep {
	out := make([]AmbiguousStep, 0, len(amb))
	for _, a := range amb {
		out = append(out, a)
	}
	sortAmbiguous(out)
	return out
}

func sortAmbiguous(steps []AmbiguousStep) {
	sort.Slice(steps, func(a, b int) bool {
		if steps[a].Index != steps[b].Index {
			return steps[a].Index < steps[b].Index
		}
		return steps[a].At < steps[b].At
	})
}

func equalAmbiguousStep(a, b AmbiguousStep) bool {
	return a.Index == b.Index && a.At == b.At && a.Role == b.Role &&
		a.Priority == b.Priority && slices.Equal(a.Candidates, b.Candidates)
}

func formatResult(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status=%s cost=%d", res.Status, res.TotalCost)
	if len(res.Steps) > 0 {
		var parts []string
		for _, st := range res.Steps {
			parts = append(parts, fmt.Sprintf("%s-[%s/%s]->%s", st.From, st.LinkType, st.Role, st.To))
		}
		fmt.Fprintf(&b, " path=%s", strings.Join(parts, " "))
	}
	for _, a := range res.Ambiguous {
		fmt.Fprintf(&b, " amb{step=%d at=%s role=%s pri=%d cand=%v}",
			a.Index, a.At, a.Role, a.Priority, a.Candidates)
	}
	return b.String()
}

func compareResults(t *testing.T, q Query, got, want Result) {
	t.Helper()
	sortAmbiguous(got.Ambiguous)
	sortAmbiguous(want.Ambiguous)
	if got.Status != want.Status {
		t.Fatalf("status mismatch: got %v want %v\nquery=%+v\ngot:  %s\nwant: %s",
			got.Status, want.Status, q, formatResult(got), formatResult(want))
	}
	if got.Status == StatusOK {
		if got.TotalCost != want.TotalCost {
			t.Fatalf("cost mismatch: got %d want %d\nquery=%+v", got.TotalCost, want.TotalCost, q)
		}
		if !slices.Equal(got.Steps, want.Steps) {
			t.Fatalf("steps mismatch:\ngot:  %s\nwant: %s\nquery=%+v",
				formatResult(got), formatResult(want), q)
		}
	}
	if !slices.EqualFunc(got.Ambiguous, want.Ambiguous, equalAmbiguousStep) {
		t.Fatalf("ambiguous mismatch:\ngot:  %v\nwant: %v\nquery=%+v",
			got.Ambiguous, want.Ambiguous, q)
	}
}

// 随机生成对象、链接类型角色与优先级配置序列，
// 将查询引擎结果与朴素穷举参照模型逐一对照，
// 并记录每次查询的输入、输出与歧义步定位。
func TestRandomizedAgainstNaive(t *testing.T) {
	const iterations = 500
	roleAlphabet := []Role{"r0", "r1", "r2"}

	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			s := NewStore()

			numObjs := 3 + rng.Intn(3)
			objs := make([]ObjectID, numObjs)
			for i := range objs {
				objs[i] = ObjectID(fmt.Sprintf("O%d", i))
				s.AddObject(objs[i])
			}

			numTypes := 3 + rng.Intn(4)
			types := make([]LinkTypeName, numTypes)
			for i := range types {
				types[i] = LinkTypeName(fmt.Sprintf("T%d", i))
				s.AddLinkType(types[i], int64(1+rng.Intn(4)))
				hasRole := false
				for _, r := range roleAlphabet {
					if rng.Intn(2) == 0 {
						if err := s.DeclareRole(types[i], r, rng.Intn(2)); err != nil {
							t.Fatal(err)
						}
						hasRole = true
					}
				}
				if !hasRole {
					if err := s.DeclareRole(types[i], roleAlphabet[rng.Intn(len(roleAlphabet))], rng.Intn(2)); err != nil {
						t.Fatal(err)
					}
				}
				for j := 0; j < 1+rng.Intn(4); j++ {
					from := objs[rng.Intn(numObjs)]
					to := objs[rng.Intn(numObjs)]
					if err := s.AddLink(types[i], from, to); err != nil {
						t.Fatal(err)
					}
				}
			}

			// 随机权限：存在性按对象、遍历按链接类型授予。
			for _, o := range objs {
				if rng.Intn(10) < 8 {
					s.GrantExistence(testCaller, o)
				}
			}
			for _, typ := range types {
				if rng.Intn(10) < 9 {
					s.GrantTraversal(testCaller, typ)
				}
			}

			// 随机配置变更序列：角色声明、优先级调整、链接增删、权限变更。
			for m := 0; m < rng.Intn(6); m++ {
				typ := types[rng.Intn(numTypes)]
				role := roleAlphabet[rng.Intn(len(roleAlphabet))]
				switch rng.Intn(6) {
				case 0:
					if err := s.DeclareRole(typ, role, rng.Intn(2)); err != nil {
						t.Fatal(err)
					}
				case 1:
					_ = s.SetRolePriority(typ, role, rng.Intn(2))
				case 2:
					_ = s.AddLink(typ, objs[rng.Intn(numObjs)], objs[rng.Intn(numObjs)])
				case 3:
					s.RemoveLink(typ, objs[rng.Intn(numObjs)], objs[rng.Intn(numObjs)])
				case 4:
					if rng.Intn(2) == 0 {
						s.GrantTraversal(testCaller, typ)
					} else {
						s.RevokeTraversal(testCaller, typ)
					}
				default:
					_ = s.UndeclareRole(typ, role)
				}
			}

			// 随机查询：主要从已声明角色中选取，偶尔包含空序列或未声明角色。
			q := Query{
				Caller: testCaller,
				Start:  objs[rng.Intn(numObjs)],
				End:    objs[rng.Intn(numObjs)],
			}
			// 大多数迭代显式授予起终点存在性权限，集中覆盖可达性分支。
			if rng.Intn(10) < 8 {
				s.GrantExistence(testCaller, q.Start)
				s.GrantExistence(testCaller, q.End)
			}
			var declared []Role
			for r := range s.roleRefs {
				declared = append(declared, r)
			}
			switch rng.Intn(20) {
			case 0:
				q.Roles = nil
			case 1:
				q.Roles = []Role{"ghost"}
			default:
				n := 1 + rng.Intn(3)
				for i := 0; i < n; i++ {
					if len(declared) > 0 && rng.Intn(10) < 9 {
						q.Roles = append(q.Roles, declared[rng.Intn(len(declared))])
					} else {
						q.Roles = append(q.Roles, roleAlphabet[rng.Intn(len(roleAlphabet))])
					}
				}
			}

			got := s.Query(q)
			want := naiveQuery(s, q)
			t.Logf("input=%+v", q)
			t.Logf("engine: %s", formatResult(got))
			t.Logf("naive:  %s", formatResult(want))
			compareResults(t, q, got, want)
		})
	}
}
