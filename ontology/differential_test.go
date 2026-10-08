package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This file holds an independent, deliberately naive reference model:
// it snapshots the store, enumerates every simple path by plain DFS,
// matches category constraints with a separate split-based matcher, and
// picks the optimum by brute force. The randomized differential test
// compares the production search against this model and logs every
// query together with its input, output and deciding candidate path.

type naiveGraph struct {
	catRank   map[Category]int
	forbidden map[ObjectType]bool
	objType   map[ObjectID]ObjectType
	isolated  map[ObjectID]bool
	adj       map[ObjectID][]adjEntry
	linkTyp   map[LinkID]LinkTypeID
	linkTypes map[LinkTypeID]LinkType
}

func snapshot(s *Store) *naiveGraph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g := &naiveGraph{
		catRank:   s.catRank,
		forbidden: s.forbidden,
		objType:   map[ObjectID]ObjectType{},
		isolated:  map[ObjectID]bool{},
		adj:       map[ObjectID][]adjEntry{},
		linkTyp:   map[LinkID]LinkTypeID{},
		linkTypes: map[LinkTypeID]LinkType{},
	}
	for id, o := range s.objects {
		g.objType[id] = o.typ
		g.isolated[id] = o.isolated
		g.adj[id] = append([]adjEntry{}, o.adj...)
	}
	for id, l := range s.links {
		g.linkTyp[id] = l.typ
	}
	for id, lt := range s.linkTypes {
		g.linkTypes[id] = *lt
	}
	return g
}

// naiveMatch reports whether the category rank sequence satisfies the
// constraint, implemented as an explicit split search: the front star
// consumes nf >= 0 categories, the exact middle follows, and the back
// star consumes the rest.
func naiveMatch(cats []int, any []bool, rank []int, sf, sb bool) bool {
	k := len(any)
	match := func(pos, r int) bool { return any[pos] || rank[pos] == r }
	lo, hi := 0, k-1
	if sf {
		lo = 1
	}
	if sb {
		hi = k - 2
	}
	exact := hi - lo + 1
	if exact < 0 {
		exact = 0
	}
	n := len(cats)
	if n < exact {
		return false
	}
	maxFront := 0
	if sf {
		maxFront = n - exact
	}
	for nf := 0; nf <= maxFront; nf++ {
		ok := true
		for i := 0; i < nf && ok; i++ {
			ok = match(0, cats[i])
		}
		for i := 0; i < exact && ok; i++ {
			ok = match(lo+i, cats[nf+i])
		}
		back := cats[nf+exact:]
		if !sb && len(back) > 0 {
			ok = false
		}
		for _, r := range back {
			if !match(k-1, r) {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

type naiveCand struct {
	cost  uint64
	cats  []int
	objs  []ObjectID
	links []LinkID
}

func naiveCmp(a, b naiveCand) int {
	if a.cost != b.cost {
		if a.cost < b.cost {
			return -1
		}
		return 1
	}
	if c := cmpSeq(a.cats, b.cats); c != 0 {
		return c
	}
	return cmpSeq(a.objs, b.objs)
}

// naiveQuery solves the query by exhaustive enumeration. It re-implements
// parameter validation independently.
func naiveQuery(g *naiveGraph, q Query) (Result, error) {
	if len(q.Constraint) == 0 {
		return Result{}, fmt.Errorf("%w: empty", ErrInvalidParams)
	}
	k := len(q.Constraint)
	any := make([]bool, k)
	rank := make([]int, k)
	for i, p := range q.Constraint {
		if p.Star && i != 0 && i != k-1 {
			return Result{}, fmt.Errorf("%w: middle star", ErrInvalidParams)
		}
		any[i] = p.Any
		if !p.Any {
			r, ok := g.catRank[p.Category]
			if !ok {
				return Result{}, fmt.Errorf("%w: bad category", ErrInvalidParams)
			}
			rank[i] = r
		}
	}
	if _, ok := g.objType[q.Start]; !ok {
		return Result{}, fmt.Errorf("%w: no start", ErrInvalidParams)
	}
	if _, ok := g.objType[q.End]; !ok {
		return Result{}, fmt.Errorf("%w: no end", ErrInvalidParams)
	}
	if g.forbidden[g.objType[q.Start]] || g.forbidden[g.objType[q.End]] {
		return Result{}, ErrForbiddenType
	}
	sf := q.Constraint[0].Star
	sb := q.Constraint[k-1].Star

	if q.Start == q.End {
		if naiveMatch(nil, any, rank, sf, sb) {
			return Result{Found: true, Path: Path{Objects: []ObjectID{q.Start}}}, nil
		}
		return Result{}, nil
	}

	var cands []naiveCand
	visited := map[ObjectID]bool{q.Start: true}
	var dfs func(u ObjectID, cats []int, objs []ObjectID, links []LinkID, cost uint64)
	dfs = func(u ObjectID, cats []int, objs []ObjectID, links []LinkID, cost uint64) {
		if u == q.End {
			if naiveMatch(cats, any, rank, sf, sb) {
				cands = append(cands, naiveCand{cost, cats, objs, links})
			}
			return
		}
		for _, e := range g.adj[u] {
			w := e.to
			if visited[w] {
				continue
			}
			if w != q.End && g.isolated[w] {
				continue
			}
			lt := g.linkTypes[g.linkTyp[e.link]]
			if lt.Principals != nil && !lt.Principals[q.Principal] {
				continue
			}
			visited[w] = true
			dfs(w,
				append(append([]int{}, cats...), g.catRank[lt.Category]),
				append(append([]ObjectID{}, objs...), w),
				append(append([]LinkID{}, links...), e.link),
				cost+uint64(lt.Cost))
			delete(visited, w)
		}
	}
	dfs(q.Start, nil, []ObjectID{q.Start}, nil, 0)

	if len(cands) == 0 {
		return Result{}, nil
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if naiveCmp(c, best) < 0 {
			best = c
		}
	}
	linkKeys := map[string]bool{}
	rep := best
	for _, c := range cands {
		if naiveCmp(c, best) == 0 {
			linkKeys[linkKey(c.links)] = true
			if cmpSeq(c.links, rep.links) < 0 {
				rep = c
			}
		}
	}
	rankToCat := make([]Category, len(g.catRank))
	for c, r := range g.catRank {
		rankToCat[r] = c
	}
	cats := make([]Category, len(rep.cats))
	for i, r := range rep.cats {
		cats[i] = rankToCat[r]
	}
	return Result{
		Found:      true,
		Equivalent: len(linkKeys) > 1,
		Path: Path{
			Objects:    rep.objs,
			Links:      rep.links,
			Categories: cats,
			TotalCost:  rep.cost,
		},
	}, nil
}

// randomStore builds a random store: random link types (directed or
// bidirectional, possibly permission-restricted, cost 0..6), random
// objects, random links (duplicates allowed), random isolation flags
// and occasionally a forbidden object type.
func randomStore(t *testing.T, rng *rand.Rand) *Store {
	t.Helper()
	cats := []Category{"a", "b", "c"}
	typs := []ObjectType{"t0", "t1", "t2"}
	s, err := NewStore(cats, typs)
	if err != nil {
		t.Fatal(err)
	}
	nTypes := 2 + rng.Intn(4)
	for i := 0; i < nTypes; i++ {
		lt := LinkType{
			ID:            LinkTypeID(fmt.Sprintf("LT%d", i)),
			SrcType:       typs[rng.Intn(len(typs))],
			DstType:       typs[rng.Intn(len(typs))],
			Bidirectional: rng.Intn(3) == 0,
			Category:      cats[rng.Intn(len(cats))],
			Cost:          uint32(rng.Intn(7)),
		}
		if rng.Intn(3) == 0 {
			lt.Principals = map[Principal]bool{}
			for _, p := range []Principal{"p0", "p1"} {
				if rng.Intn(2) == 0 {
					lt.Principals[p] = true
				}
			}
		}
		if err := s.AddLinkType(lt); err != nil {
			t.Fatal(err)
		}
	}
	// At least two objects per type so links can always be placed.
	var objs []ObjectID
	for _, tp := range typs {
		for i := 0; i < 2; i++ {
			id := ObjectID(fmt.Sprintf("o%s_%d", tp, i))
			if err := s.AddObject(id, tp); err != nil {
				t.Fatal(err)
			}
			objs = append(objs, id)
		}
	}
	for i := 0; i < 1+rng.Intn(4); i++ {
		id := ObjectID(fmt.Sprintf("ox%d", i))
		if err := s.AddObject(id, typs[rng.Intn(len(typs))]); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, id)
	}
	byType := map[ObjectType][]ObjectID{}
	for _, id := range objs {
		byType[s.objects[id].typ] = append(byType[s.objects[id].typ], id)
	}
	nLinks := 4 + rng.Intn(10)
	for i := 0; i < nLinks; i++ {
		ltID := LinkTypeID(fmt.Sprintf("LT%d", rng.Intn(nTypes)))
		lt := s.linkTypes[ltID]
		srcs, dsts := byType[lt.SrcType], byType[lt.DstType]
		src := srcs[rng.Intn(len(srcs))]
		dst := dsts[rng.Intn(len(dsts))]
		if src == dst {
			continue // keep the generator simple: no self loops
		}
		if err := s.AddLink(LinkID(fmt.Sprintf("l%d", i)), ltID, src, dst); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range objs {
		if rng.Intn(6) == 0 {
			if err := s.SetIsolation(id, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if rng.Intn(8) == 0 {
		if err := s.SetTypeForbidden(typs[rng.Intn(len(typs))], true); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func randomQuery(rng *rand.Rand, objs []ObjectID) Query {
	pick := func() ObjectID {
		if rng.Intn(10) == 0 {
			return "ghost" // occasionally a nonexistent object
		}
		return objs[rng.Intn(len(objs))]
	}
	q := Query{
		Start:     pick(),
		End:       pick(),
		Principal: Principal(fmt.Sprintf("p%d", rng.Intn(3))),
	}
	n := rng.Intn(5) // 0..4 positions; 0 is invalid on purpose
	cats := []Category{"a", "b", "c", "zz"}
	for i := 0; i < n; i++ {
		var p ConstraintPos
		if rng.Intn(2) == 0 {
			p.Any = true
		} else {
			p.Category = cats[rng.Intn(len(cats))]
		}
		if rng.Intn(4) == 0 {
			p.Star = true // also on middle positions: sometimes invalid
		}
		q.Constraint = append(q.Constraint, p)
	}
	return q
}

func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParams):
		return "invalid"
	case errors.Is(err, ErrForbiddenType):
		return "forbidden"
	default:
		return "other:" + err.Error()
	}
}

func TestDifferentialAgainstNaive(t *testing.T) {
	const seeds = 20000
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := randomStore(t, rng)
		var objs []ObjectID
		for id := range s.objects {
			objs = append(objs, id)
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i] < objs[j] })
		g := snapshot(s)
		for qi := 0; qi < 3; qi++ {
			q := randomQuery(rng, objs)
			got, gotErr := s.Query(q)
			want, wantErr := naiveQuery(g, q)
			t.Logf("seed=%d q=%d in=%+v out=%+v err=%v naive=%+v nerr=%v",
				seed, qi, q, got, gotErr, want, wantErr)
			if errKind(gotErr) != errKind(wantErr) {
				t.Fatalf("seed=%d q=%+v: error kind %s (%v) != naive %s (%v)",
					seed, q, errKind(gotErr), gotErr, errKind(wantErr), wantErr)
			}
			if gotErr != nil {
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d q=%+v:\n got=%+v\nwant=%+v", seed, q, got, want)
			}
		}
	}
}
