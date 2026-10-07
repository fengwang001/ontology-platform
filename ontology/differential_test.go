package ontology

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 差分测试：在大量随机生成的关系图与动作序列上，
// 将 Engine 的判定与执行结果同独立维护的朴素参照实现逐项对照。

// hashBool 构造与调用顺序无关的确定性伪随机判定，
// 保证 Engine 与参照实现在不同调用顺序下得到相同结论。
func hashBool(seed int64, threshold int, parts ...string) bool {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d", seed)
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return int(h.Sum64()%100) < threshold
}

type randomWorld struct {
	store *Store
	types []ObjectTypeID
	links []LinkTypeID
	insts []InstanceID
	auth  Authorizer
}

func genWorld(t *testing.T, rng *rand.Rand, seed int64) *randomWorld {
	t.Helper()
	s := NewStore()
	nTypes := 1 + rng.Intn(3)
	types := make([]ObjectTypeID, nTypes)
	for i := range types {
		types[i] = ObjectTypeID(fmt.Sprintf("T%d", i))
		s.AddObjectType(ObjectType{ID: types[i], Properties: []string{"p0", "p1"}})
	}
	var linkTypes []LinkTypeID
	for i := 0; i < rng.Intn(4); i++ {
		lt := LinkType{
			ID:   LinkTypeID(fmt.Sprintf("L%d", i)),
			From: types[rng.Intn(nTypes)],
			To:   types[rng.Intn(nTypes)],
		}
		s.AddLinkType(lt)
		linkTypes = append(linkTypes, lt.ID)
	}
	byType := map[ObjectTypeID][]InstanceID{}
	var insts []InstanceID
	for i := 0; i < 1+rng.Intn(12); i++ {
		id := InstanceID(fmt.Sprintf("i%d", i))
		typ := types[rng.Intn(nTypes)]
		s.PutInstance(&Instance{ID: id, Type: typ, Props: map[string]string{"p0": "v"}, Version: 1})
		byType[typ] = append(byType[typ], id)
		insts = append(insts, id)
	}
	for i := 0; i < rng.Intn(20); i++ {
		if len(linkTypes) == 0 {
			break
		}
		lt := s.LinkTypes[linkTypes[rng.Intn(len(linkTypes))]]
		froms, tos := byType[lt.From], byType[lt.To]
		if len(froms) == 0 || len(tos) == 0 {
			continue
		}
		s.AddLink(Link{Type: lt.ID, From: froms[rng.Intn(len(froms))], To: tos[rng.Intn(len(tos))]})
	}
	auth := AuthorizerFunc{
		VisibleFn: func(sub SubjectID, id InstanceID) bool {
			return hashBool(seed, 80, "vis", string(sub), string(id))
		},
		AuthorizeFn: func(sub SubjectID, id InstanceID, d int) bool {
			return hashBool(seed, 70, "gr", string(sub), string(id), fmt.Sprint(d))
		},
	}
	return &randomWorld{store: s, types: types, links: linkTypes, insts: insts, auth: auth}
}

func genDecl(rng *rand.Rand, w *randomWorld, idx int) ActionDecl {
	ops := map[ObjectTypeID][]OpKind{}
	for _, typ := range w.types {
		var kinds []OpKind
		if rng.Intn(2) == 0 {
			kinds = append(kinds, OpModify)
		}
		if rng.Intn(2) == 0 {
			kinds = append(kinds, OpCreate)
		}
		if len(kinds) > 0 {
			ops[typ] = kinds
		}
	}
	var excluded []LinkTypeID
	for _, lt := range w.links {
		if rng.Intn(2) == 0 {
			excluded = append(excluded, lt)
		}
	}
	mode := InvisibleDeny
	if rng.Intn(2) == 0 {
		mode = InvisibleSkip
	}
	merge := MergeAll
	if rng.Intn(2) == 0 {
		merge = MergeAny
	}
	return ActionDecl{
		ID:         ActionTypeID(fmt.Sprintf("act%d", idx)),
		AllowedOps: ops,
		Cascade:    CascadeRule{MaxDepth: rng.Intn(5), ExcludeLinkTypes: excluded},
		Invisible:  mode,
		Merge:      merge,
	}
}

func genInvocation(rng *rand.Rand, w *randomWorld, d ActionDecl, seq int) Invocation {
	inv := Invocation{Action: d.ID}
	for i := 0; i < 1+rng.Intn(3); i++ {
		typ := w.types[rng.Intn(len(w.types))]
		kind := OpModify
		if rng.Intn(3) == 0 {
			kind = OpCreate
		}
		op := DirectOp{Kind: kind, Type: typ, Props: map[string]string{"p0": fmt.Sprintf("w%d", seq)}}
		if kind == OpCreate {
			op.Target = InstanceID(fmt.Sprintf("new%d-%d", seq, i))
		} else {
			var candidates []InstanceID
			for _, id := range w.insts {
				if w.store.Instances[id].Type == typ {
					candidates = append(candidates, id)
				}
			}
			if len(candidates) == 0 {
				continue
			}
			op.Target = candidates[rng.Intn(len(candidates))]
		}
		inv.Ops = append(inv.Ops, op)
	}
	return inv
}

func sortedIDs(ids []InstanceID) []InstanceID {
	out := append([]InstanceID{}, ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestDifferentialAgainstReference(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1337, 20261007} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			for g := 0; g < 8; g++ {
				w := genWorld(t, rng, seed)
				eng := NewEngine(w.store, w.auth)
				decls := map[ActionTypeID]ActionDecl{}
				for i := 0; i < 2; i++ {
					d := genDecl(rng, w, i)
					decls[d.ID] = d
					eng.RegisterAction(d)
				}
				declList := make([]ActionDecl, 0, len(decls))
				for _, d := range decls {
					declList = append(declList, d)
				}
				subjects := []SubjectID{"s0", "s1"}
				for step := 0; step < 40; step++ {
					d := declList[rng.Intn(len(declList))]
					inv := genInvocation(rng, w, d, step)
					subj := subjects[rng.Intn(len(subjects))]

					want := ReferenceExecute(w.store, decls, w.auth, subj, inv)
					allowed, err := eng.Execute(subj, inv)

					var gotErr ErrorKind
					if err != nil {
						gotErr = err.Kind
					}
					if allowed != want.Allowed || gotErr != want.ErrKind {
						t.Fatalf("graph %d step %d: engine(allowed=%v err=%v) != reference(allowed=%v err=%v)\ninv=%+v\ndecl=%+v",
							g, step, allowed, gotErr, want.Allowed, want.ErrKind, inv, d)
					}
					recs := eng.Log()
					rec := recs[len(recs)-1]
					var gotSkipped []InstanceID
					for _, sk := range rec.Skipped {
						gotSkipped = append(gotSkipped, sk.Instance)
					}
					if !reflect.DeepEqual(sortedIDs(gotSkipped), sortedIDs(want.Skipped)) {
						t.Fatalf("graph %d step %d: skipped %v != reference %v", g, step, gotSkipped, want.Skipped)
					}
					got := w.store.Snapshot()
					if !reflect.DeepEqual(got, want.State) {
						t.Fatalf("graph %d step %d: state mismatch after inv=%+v", g, step, inv)
					}
				}
			}
		})
	}
}
