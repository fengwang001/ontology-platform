package siblings

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// canonKey is the canonical, alias-free view of one key used for
// field-by-field comparison.
type canonKey struct {
	Sibs []Sibling
	Ctx  map[string]uint64
}

func canon(s *Store) map[string]canonKey {
	out := make(map[string]canonKey)
	for _, k := range s.Keys() {
		sibs, ctx := s.Get(k)
		out[k] = canonKey{Sibs: sibs, Ctx: ctx}
	}
	return out
}

func sortedCtx(ctx map[string]uint64) string {
	nodes := make([]string, 0, len(ctx))
	for n := range ctx {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	var b strings.Builder
	b.WriteString("{")
	for i, n := range nodes {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s:%d", n, ctx[n])
	}
	b.WriteString("}")
	return b.String()
}

func canonString(c map[string]canonKey) string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		ck := c[k]
		fmt.Fprintf(&b, "  key %q ctx=%s\n", k, sortedCtx(ck.Ctx))
		for _, sib := range ck.Sibs {
			fmt.Fprintf(&b, "    (%s,%d)=%q\n", sib.Dot.ID, sib.Dot.N, sib.Value)
		}
	}
	return b.String()
}

func mustStore(t *testing.T, capacity int) *Store {
	t.Helper()
	s, err := NewStore(capacity)
	if err != nil {
		t.Fatalf("NewStore(%d): %v", capacity, err)
	}
	return s
}

func mustPut(t *testing.T, s *Store, key, id string, ctx map[string]uint64, value string) {
	t.Helper()
	if err := s.Put(key, id, ctx, value); err != nil {
		t.Fatalf("Put(%q, %q, %s, %q): %v", key, id, sortedCtx(ctx), value, err)
	}
}

func checkGet(t *testing.T, s *Store, key string, wantSibs []Sibling, wantCtx map[string]uint64) {
	t.Helper()
	sibs, ctx := s.Get(key)
	if !reflect.DeepEqual(sibs, wantSibs) {
		t.Fatalf("Get(%q) siblings = %v, want %v", key, sibs, wantSibs)
	}
	if !reflect.DeepEqual(ctx, wantCtx) {
		t.Fatalf("Get(%q) ctx = %v, want %v", key, ctx, wantCtx)
	}
}

// naiveStore is an independent, deliberately simple step-by-step model of
// the spec, used as a reference to cross-check Store.
type naiveStore struct {
	cap  int
	keys map[string]*naiveKey
}

type naiveKey struct {
	sibs map[Dot]string
	seen map[string]uint64
}

func newNaive(capacity int) *naiveStore {
	return &naiveStore{cap: capacity, keys: make(map[string]*naiveKey)}
}

func (m *naiveStore) clone() *naiveStore {
	cp := newNaive(m.cap)
	for k, nk := range m.keys {
		sibs := make(map[Dot]string, len(nk.sibs))
		for d, v := range nk.sibs {
			sibs[d] = v
		}
		seen := make(map[string]uint64, len(nk.seen))
		for n, c := range nk.seen {
			seen[n] = c
		}
		cp.keys[k] = &naiveKey{sibs: sibs, seen: seen}
	}
	return cp
}

func (m *naiveStore) put(key, id string, ctx map[string]uint64, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if id == "" {
		return ErrEmptyID
	}
	for node := range ctx {
		if node == "" {
			return ErrEmptyCtxNode
		}
	}
	nk := m.keys[key]
	seen := map[string]uint64{}
	if nk != nil {
		seen = nk.seen
	}
	for node, c := range ctx {
		if c > seen[node] {
			return ErrCtxAhead
		}
	}
	kept := map[Dot]string{}
	if nk != nil {
		for d, v := range nk.sibs {
			if ctx[d.ID] >= d.N {
				continue
			}
			kept[d] = v
		}
	}
	if len(kept)+1 > m.cap {
		return ErrCapExceeded
	}
	n := seen[id] + 1
	kept[Dot{ID: id, N: n}] = value
	if nk == nil {
		nk = &naiveKey{sibs: map[Dot]string{}, seen: map[string]uint64{}}
		m.keys[key] = nk
	}
	nk.sibs = kept
	nk.seen[id] = n
	return nil
}

func (m *naiveStore) merge(o *naiveStore) {
	for key, ok := range o.keys {
		nk := m.keys[key]
		if nk == nil {
			sibs := make(map[Dot]string, len(ok.sibs))
			for d, v := range ok.sibs {
				sibs[d] = v
			}
			seen := make(map[string]uint64, len(ok.seen))
			for n, c := range ok.seen {
				seen[n] = c
			}
			m.keys[key] = &naiveKey{sibs: sibs, seen: seen}
			continue
		}
		dots := map[Dot]string{}
		for d, v := range nk.sibs {
			if _, has := ok.sibs[d]; has || ok.seen[d.ID] < d.N {
				dots[d] = v
			}
		}
		for d, v := range ok.sibs {
			if _, has := nk.sibs[d]; has || nk.seen[d.ID] < d.N {
				if cur, exists := dots[d]; !exists || v > cur {
					dots[d] = v
				}
			}
		}
		nk.sibs = dots
		for n, c := range ok.seen {
			if c > nk.seen[n] {
				nk.seen[n] = c
			}
		}
	}
}

func (m *naiveStore) canon() map[string]canonKey {
	out := make(map[string]canonKey)
	for k, nk := range m.keys {
		sibs := make([]Sibling, 0, len(nk.sibs))
		for d, v := range nk.sibs {
			sibs = append(sibs, Sibling{Dot: d, Value: v})
		}
		sort.Slice(sibs, func(i, j int) bool {
			if sibs[i].Dot.ID != sibs[j].Dot.ID {
				return sibs[i].Dot.ID < sibs[j].Dot.ID
			}
			return sibs[i].Dot.N < sibs[j].Dot.N
		})
		ctx := make(map[string]uint64, len(nk.seen))
		for n, c := range nk.seen {
			ctx[n] = c
		}
		out[k] = canonKey{Sibs: sibs, Ctx: ctx}
	}
	return out
}

func TestNewStoreRejectsBadCap(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		if _, err := NewStore(c); !errors.Is(err, ErrInvalidCap) {
			t.Errorf("NewStore(%d) err = %v, want ErrInvalidCap", c, err)
		}
	}
	if _, err := NewStore(1); err != nil {
		t.Errorf("NewStore(1) err = %v, want nil", err)
	}
}

func TestPutPruneEqualAndOneBelow(t *testing.T) {
	s := mustStore(t, 10)
	mustPut(t, s, "k", "A", nil, "a1") // dot (A,1)

	// ctx[A]=0 is one below m=1: the sibling survives.
	mustPut(t, s, "k", "B", map[string]uint64{"A": 0}, "b1")
	sibs, ctx := s.Get("k")
	t.Logf("input: Put(k, B, ctx={A:0}); output: siblings=%v ctx=%s; basis: ctx[A]=0 < m=1, (A,1) not covered", sibs, sortedCtx(ctx))
	checkGet(t, s, "k",
		[]Sibling{{Dot{ID: "A", N: 1}, "a1"}, {Dot{ID: "B", N: 1}, "b1"}},
		map[string]uint64{"A": 1, "B": 1})

	// ctx[A]=1 equals m=1: the sibling (A,1) is covered and removed.
	mustPut(t, s, "k", "C", map[string]uint64{"A": 1}, "c1")
	sibs, ctx = s.Get("k")
	t.Logf("input: Put(k, C, ctx={A:1}); output: siblings=%v ctx=%s; basis: ctx[A]=1 >= m=1, (A,1) covered and pruned", sibs, sortedCtx(ctx))
	checkGet(t, s, "k",
		[]Sibling{{Dot{ID: "B", N: 1}, "b1"}, {Dot{ID: "C", N: 1}, "c1"}},
		map[string]uint64{"A": 1, "B": 1, "C": 1})
}

func TestEmptyCtxDotStillIncrements(t *testing.T) {
	s := mustStore(t, 10)
	mustPut(t, s, "k", "A", nil, "x")
	mustPut(t, s, "k", "A", map[string]uint64{}, "y")
	sibs, ctx := s.Get("k")
	t.Logf("two empty-ctx writes by A; output: siblings=%v ctx=%s; basis: new dot is (A, seen[A]+1)=(A,2), not a duplicate (A,1)", sibs, sortedCtx(ctx))
	checkGet(t, s, "k",
		[]Sibling{{Dot{ID: "A", N: 1}, "x"}, {Dot{ID: "A", N: 2}, "y"}},
		map[string]uint64{"A": 2})
}

func TestStaleCtxConcurrentSiblingsAndFullCtxOverwrite(t *testing.T) {
	s := mustStore(t, 10)
	mustPut(t, s, "k", "A", nil, "a1")
	// B writes with a stale (empty) context: concurrent sibling, (A,1) kept.
	mustPut(t, s, "k", "B", nil, "b1")
	sibs, ctx := s.Get("k")
	t.Logf("stale-ctx write by B; output: siblings=%v; basis: empty ctx covers nothing, (A,1) and (B,1) are concurrent", sibs)
	checkGet(t, s, "k",
		[]Sibling{{Dot{ID: "A", N: 1}, "a1"}, {Dot{ID: "B", N: 1}, "b1"}},
		map[string]uint64{"A": 1, "B": 1})

	// A writes again with the full context: every sibling is covered.
	_, full := s.Get("k")
	mustPut(t, s, "k", "A", full, "a2")
	sibs, ctx = s.Get("k")
	t.Logf("full-ctx write by A; output: siblings=%v ctx=%s; basis: ctx={A:1,B:1} covers (A,1) and (B,1), only (A,2) remains", sibs, sortedCtx(ctx))
	checkGet(t, s, "k",
		[]Sibling{{Dot{ID: "A", N: 2}, "a2"}},
		map[string]uint64{"A": 2, "B": 1})
}

func TestMergeCoveredRemovedUnseenKept(t *testing.T) {
	r1 := mustStore(t, 10)
	r2 := mustStore(t, 10)
	mustPut(t, r1, "k", "A", nil, "a1")
	mustPut(t, r2, "k", "B", nil, "b1")

	r1.Merge(r2)
	sibs, ctx := r1.Get("k")
	t.Logf("r1.Merge(r2): siblings=%v; basis: neither side has seen the other's dot, both kept", sibs)
	checkGet(t, r1, "k",
		[]Sibling{{Dot{ID: "A", N: 1}, "a1"}, {Dot{ID: "B", N: 1}, "b1"}},
		map[string]uint64{"A": 1, "B": 1})

	// r2 catches up, then overwrites everything with a full-context write.
	r2.Merge(r1)
	_, full := r2.Get("k")
	mustPut(t, r2, "k", "B", full, "b2")
	checkGet(t, r2, "k",
		[]Sibling{{Dot{ID: "B", N: 2}, "b2"}},
		map[string]uint64{"A": 1, "B": 2})

	r1.Merge(r2)
	sibs, ctx = r1.Get("k")
	t.Logf("r1.Merge(r2) again: siblings=%v ctx=%s; basis: (A,1) and (B,1) covered by r2 seen {A:1,B:2} and dropped; (B,2) unseen by r1 (seen[B]=1 < 2) and kept", sibs, sortedCtx(ctx))
	checkGet(t, r1, "k",
		[]Sibling{{Dot{ID: "B", N: 2}, "b2"}},
		map[string]uint64{"A": 1, "B": 2})
}

func TestMergeSameDotValueConflict(t *testing.T) {
	r1 := mustStore(t, 10)
	r2 := mustStore(t, 10)
	mustPut(t, r1, "k", "A", nil, "apple")
	mustPut(t, r2, "k", "A", nil, "banana")
	r1.Merge(r2)
	sibs, _ := r1.Get("k")
	t.Logf("same dot (A,1) on both sides with different values; output: %v; basis: dedup by dot, lexicographically larger value wins", sibs)
	checkGet(t, r1, "k",
		[]Sibling{{Dot{ID: "A", N: 1}, "banana"}},
		map[string]uint64{"A": 1})
	// Symmetric direction must agree.
	r2.Merge(r1)
	checkGet(t, r2, "k",
		[]Sibling{{Dot{ID: "A", N: 1}, "banana"}},
		map[string]uint64{"A": 1})
}

func TestCapCheckedAfterPruning(t *testing.T) {
	s := mustStore(t, 1)
	mustPut(t, s, "k", "A", nil, "a1")

	err := s.Put("k", "A", nil, "a2")
	if !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("empty-ctx second write err = %v, want ErrCapExceeded", err)
	}
	t.Logf("empty-ctx write rejected: %v; basis: empty ctx prunes nothing, 1 kept + 1 new = 2 > Cap=1", err)

	mustPut(t, s, "k", "A", map[string]uint64{"A": 1}, "a3")
	sibs, _ := s.Get("k")
	t.Logf("full-ctx write accepted: siblings=%v; basis: (A,1) pruned first, 0 kept + 1 new = 1 <= Cap=1", sibs)
	checkGet(t, s, "k",
		[]Sibling{{Dot{ID: "A", N: 2}, "a3"}},
		map[string]uint64{"A": 2})
}

func TestRejectedPutKeepsStateAndErrorOrder(t *testing.T) {
	s := mustStore(t, 1)
	mustPut(t, s, "k", "A", nil, "a1")
	before := canon(s)

	cases := []struct {
		name string
		key  string
		id   string
		ctx  map[string]uint64
		want error
	}{
		{"empty key reported before empty id", "", "", nil, ErrEmptyKey},
		{"empty id reported before ctx errors", "k", "", map[string]uint64{"": 9}, ErrEmptyID},
		{"empty ctx node reported before ctx-ahead", "k", "B", map[string]uint64{"": 9}, ErrEmptyCtxNode},
		{"ctx-ahead reported before cap", "k", "B", map[string]uint64{"A": 2}, ErrCtxAhead},
		{"cap exceeded after pruning", "k", "B", nil, ErrCapExceeded},
	}
	for _, tc := range cases {
		err := s.Put(tc.key, tc.id, tc.ctx, "x")
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		t.Logf("input: Put(%q, %q, %s); output: %v; basis: %s", tc.key, tc.id, sortedCtx(tc.ctx), err, tc.name)
		if after := canon(s); !reflect.DeepEqual(after, before) {
			t.Errorf("%s: rejected Put changed the state", tc.name)
		}
	}
}

func TestGetMissingKey(t *testing.T) {
	s := mustStore(t, 2)
	sibs, ctx := s.Get("nope")
	if len(sibs) != 0 || len(ctx) != 0 {
		t.Fatalf("Get(missing) = %v, %v; want empty siblings and empty ctx", sibs, ctx)
	}
	if keys := s.Keys(); len(keys) != 0 {
		t.Fatalf("Get(missing) created state: keys = %v", keys)
	}
}

func TestNoAliasing(t *testing.T) {
	s := mustStore(t, 4)
	mustPut(t, s, "k", "A", nil, "a1")
	sibs, ctx := s.Get("k")
	sibs[0].Value = "corrupted"
	ctx["A"] = 99
	ctx["evil"] = 1
	checkGet(t, s, "k",
		[]Sibling{{Dot{ID: "A", N: 1}, "a1"}},
		map[string]uint64{"A": 1})

	// Merge must deep-copy keys that exist only on the other side.
	src := mustStore(t, 4)
	mustPut(t, src, "only", "B", nil, "b1")
	dst := mustStore(t, 4)
	dst.Merge(src)
	mustPut(t, src, "only", "B", map[string]uint64{"B": 1}, "b2")
	checkGet(t, dst, "only",
		[]Sibling{{Dot{ID: "B", N: 1}, "b1"}},
		map[string]uint64{"B": 1})
}

func TestSelfMergeIsNoOp(t *testing.T) {
	s := mustStore(t, 2)
	mustPut(t, s, "k", "A", nil, "a1")
	before := canon(s)
	s.Merge(s)   // must neither deadlock nor change anything
	s.Merge(nil) // tolerated as a no-op
	if !reflect.DeepEqual(canon(s), before) {
		t.Fatal("self-merge changed the state")
	}
}

// convergeStores merges every ordered pair until no store changes.
func convergeStores(t *testing.T, ss []*Store) {
	t.Helper()
	for round := 0; round < 10; round++ {
		changed := false
		for i := range ss {
			before := canon(ss[i])
			for j := range ss {
				if i != j {
					ss[i].Merge(ss[j])
				}
			}
			if !reflect.DeepEqual(canon(ss[i]), before) {
				changed = true
			}
		}
		if !changed {
			return
		}
	}
	t.Fatal("replicas did not converge")
}

func convergeNaives(t *testing.T, ns []*naiveStore) {
	t.Helper()
	for round := 0; round < 10; round++ {
		changed := false
		for i := range ns {
			before := ns[i].canon()
			for j := range ns {
				if i != j {
					ns[i].merge(ns[j])
				}
			}
			if !reflect.DeepEqual(ns[i].canon(), before) {
				changed = true
			}
		}
		if !changed {
			return
		}
	}
	t.Fatal("naive replicas did not converge")
}

func TestThreeReplicasRandom(t *testing.T) {
	const (
		nReplicas = 3
		nPuts     = 60
		nMerges   = 40
		capacity  = 16
	)
	rng := rand.New(rand.NewPCG(42, 1080))
	keys := []string{"k1", "k2", "k3"}
	nodes := []string{"A", "B", "C"}

	stores := make([]*Store, nReplicas)
	naives := make([]*naiveStore, nReplicas)
	for i := range stores {
		stores[i] = mustStore(t, capacity)
		naives[i] = newNaive(capacity)
	}

	total := nPuts + nMerges
	for op := 0; op < total; op++ {
		r := rng.IntN(nReplicas)
		if op%5 < 3 { // 60% puts, 40% merges
			key := keys[rng.IntN(len(keys))]
			id := nodes[rng.IntN(len(nodes))]
			value := fmt.Sprintf("v%03d", op)
			var ctx map[string]uint64
			var mode string
			switch rng.IntN(3) {
			case 0:
				ctx = nil
				mode = "empty"
			case 1:
				_, ctx = stores[r].Get(key)
				mode = "full"
			default:
				_, full := stores[r].Get(key)
				ctx = make(map[string]uint64, len(full))
				for n, c := range full {
					if rng.IntN(2) == 0 {
						continue
					}
					if c > 0 && rng.IntN(2) == 0 {
						c -= uint64(rng.IntN(int(c) + 1))
					}
					ctx[n] = c
				}
				mode = "stale"
			}
			err := stores[r].Put(key, id, ctx, value)
			nerr := naives[r].put(key, id, ctx, value)
			if !errors.Is(err, nerr) {
				t.Fatalf("op %d: store err %v != naive err %v", op, err, nerr)
			}
			t.Logf("op %02d Put r%d key=%s id=%s ctx=%s (%s) value=%q -> err=%v, naive err=%v",
				op, r, key, id, sortedCtx(ctx), mode, value, err, nerr)
		} else {
			o := rng.IntN(nReplicas)
			for o == r {
				o = rng.IntN(nReplicas)
			}
			stores[r].Merge(stores[o])
			naives[r].merge(naives[o])
			t.Logf("op %02d Merge r%d <- r%d", op, r, o)
		}
	}

	// Snapshot the post-op divergence before converging.
	snaps := make([]*Store, nReplicas)
	for i := range stores {
		snaps[i] = stores[i].snapshot()
	}

	convergeStores(t, stores)
	convergeNaives(t, naives)
	want := naives[0].canon()
	for i := range stores {
		if got := canon(stores[i]); !reflect.DeepEqual(got, want) {
			t.Fatalf("replica %d mismatch\nstore:\n%s\nnaive:\n%s", i, canonString(got), canonString(want))
		}
		if got := naives[i].canon(); !reflect.DeepEqual(got, want) {
			t.Fatalf("naive replica %d mismatch", i)
		}
	}
	t.Logf("converged state (all replicas identical, matches naive model):\n%s", canonString(want))

	// Any order and any number of merges must yield the identical state.
	for trial := 0; trial < 5; trial++ {
		tr := rand.New(rand.NewPCG(uint64(trial), 999))
		ss := make([]*Store, nReplicas)
		for i := range ss {
			ss[i] = snaps[i].snapshot()
		}
		for m := 0; m < 25; m++ {
			a, b := tr.IntN(nReplicas), tr.IntN(nReplicas)
			if a != b {
				ss[a].Merge(ss[b])
			}
		}
		convergeStores(t, ss)
		for i := range ss {
			if got := canon(ss[i]); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d replica %d: merge-order-dependent state\ngot:\n%s\nwant:\n%s",
					trial, i, canonString(got), canonString(want))
			}
		}
		t.Logf("trial %d: random merge order converged to the identical state", trial)
	}

	// Commutativity: a∪b == b∪a.
	ab := snaps[0].snapshot()
	ab.Merge(snaps[1])
	ba := snaps[1].snapshot()
	ba.Merge(snaps[0])
	if !reflect.DeepEqual(canon(ab), canon(ba)) {
		t.Fatal("merge is not commutative")
	}
	// Associativity: (a∪b)∪c == a∪(b∪c).
	left := snaps[0].snapshot()
	left.Merge(snaps[1])
	left.Merge(snaps[2])
	bc := snaps[1].snapshot()
	bc.Merge(snaps[2])
	right := snaps[0].snapshot()
	right.Merge(bc)
	if !reflect.DeepEqual(canon(left), canon(right)) {
		t.Fatal("merge is not associative")
	}
	// Idempotency: merging the same source twice changes nothing more.
	idem := snaps[0].snapshot()
	idem.Merge(snaps[1])
	once := canon(idem)
	idem.Merge(snaps[1])
	if !reflect.DeepEqual(canon(idem), once) {
		t.Fatal("merge is not idempotent")
	}
	t.Log("merge is commutative, associative and idempotent on the snapshot states")
}

func TestConcurrentAccess(t *testing.T) {
	const (
		nStores = 3
		workers = 8
		opsEach = 300
	)
	stores := make([]*Store, nStores)
	for i := range stores {
		stores[i] = mustStore(t, 64)
	}
	keys := []string{"k1", "k2"}
	nodes := []string{"A", "B", "C", "D"}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, 7))
			for op := 0; op < opsEach; op++ {
				i := rng.IntN(nStores)
				switch rng.IntN(4) {
				case 0, 1:
					key := keys[rng.IntN(len(keys))]
					id := nodes[rng.IntN(len(nodes))]
					var ctx map[string]uint64
					if rng.IntN(2) == 0 {
						_, ctx = stores[i].Get(key)
					}
					// Cap overflow is expected and harmless here.
					_ = stores[i].Put(key, id, ctx, "v")
				case 2:
					_, _ = stores[i].Get(keys[rng.IntN(len(keys))])
					_ = stores[i].Keys()
				default:
					// Includes self-merge and concurrent mutual merges;
					// neither may deadlock.
					j := rng.IntN(nStores)
					stores[i].Merge(stores[j])
				}
			}
		}(uint64(w))
	}
	wg.Wait()

	convergeStores(t, stores)
	base := canon(stores[0])
	for i := 1; i < nStores; i++ {
		if got := canon(stores[i]); !reflect.DeepEqual(got, base) {
			t.Fatalf("replica %d diverged after concurrent merges", i)
		}
	}
	t.Logf("concurrent run converged to one identical state:\n%s", canonString(base))
}
