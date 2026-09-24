package visible

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/snapshot"
	"ontology/txn"
)

// naiveVisible 是只在测试中使用的朴素实现：遍历事务表逐个比对，
// 仅按「已提交且提交号 < 水位」判定，不看活跃集。
func naiveVisible(r *txn.Registry, s *snapshot.Snapshot, t txn.ID) (bool, error) {
	if err := s.Use(); err != nil {
		return false, err
	}
	for _, id := range r.All() {
		st, c, err := r.Lookup(id)
		if err != nil {
			return false, err
		}
		if id == t {
			return st == txn.Committed && c < s.Watermark(), nil
		}
	}
	return false, ErrUnknown
}

func TestVisibility(t *testing.T) {
	type tc struct {
		name    string
		setup   func(*txn.Registry) (txn.ID, *snapshot.Snapshot)
		want    bool
		wantErr error
	}
	mk := func(wm uint64, active []txn.ID, owner txn.ID, commit *uint64, abort bool) func(*txn.Registry) (txn.ID, *snapshot.Snapshot) {
		return func(r *txn.Registry) (txn.ID, *snapshot.Snapshot) {
			t0 := r.Begin()
			if abort {
				r.Abort(t0)
			} else if commit != nil {
				r.Commit(t0, *commit)
			}
			s, _ := snapshot.New(wm, active, owner, 0)
			return t0, s
		}
	}
	c5, c10, c9 := uint64(5), uint64(10), uint64(9)
	cases := []tc{
		{"below watermark visible", mk(10, nil, 999, &c5, false), true, nil},
		{"equal watermark invisible", mk(10, nil, 999, &c10, false), false, nil},
		{"watermark zero invisible", mk(0, nil, 999, &c9, false), false, nil},
		{"empty active set", mk(10, []txn.ID{}, 999, &c5, false), true, nil},
		{"active in snapshot invisible", mk(10, nil, 999, nil, false), false, nil},
		{"aborted never visible", mk(100, nil, 999, nil, true), false, nil},
		{"counterexample naive-vs-real", func(r *txn.Registry) (txn.ID, *snapshot.Snapshot) {
			t0 := r.Begin()
			r.Commit(t0, 5)
			s, _ := snapshot.New(10, []txn.ID{t0}, 999, 0)
			return t0, s
		}, false, nil},
		{"own write visible to self", func(r *txn.Registry) (txn.ID, *snapshot.Snapshot) {
			me := r.Begin()
			r.Commit(me, 1)
			s, _ := snapshot.New(0, nil, me, 0)
			return me, s
		}, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := txn.NewRegistry()
			id, s := c.setup(r)
			d, err := Decide(r, s, Version{Txn: id})
			if !errors.Is(err, c.wantErr) || d.Visible != c.want {
				t.Fatalf("got visible=%v err=%v, want %v %v", d.Visible, err, c.want, c.wantErr)
			}
			if c.name == "counterexample naive-vs-real" {
				nv, _ := naiveVisible(r, s, id)
				if !nv {
					t.Fatal("naive rule must say visible for counterexample")
				}
			}
		})
	}
}

func TestErrors(t *testing.T) {
	r := txn.NewRegistry()
	s, _ := snapshot.New(10, nil, 999, 0)
	if _, err := Decide(r, s, Version{Txn: 42}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	s.Release()
	id := r.Begin()
	if _, err := Decide(r, s, Version{Txn: id}); !errors.Is(err, ErrReleased) {
		t.Fatalf("released: %v", err)
	}
	if err := CheckChain(r, []Version{{Txn: 42}}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("chain with unknown txn: %v", err)
	}
	cr := txn.NewRegistry()
	a, b := cr.Begin(), cr.Begin()
	cr.Commit(a, 5)
	cr.Commit(b, 5) // 非严格递减 -> 损坏
	if err := CheckChain(cr, []Version{{Txn: a}, {Txn: b}}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt chain: %v", err)
	}
	if _, err := snapshot.New(1, []txn.ID{1, 2, 3}, 999, 2); !errors.Is(err, ErrActiveLimit) {
		t.Fatalf("active limit: %v", err)
	}
}

func TestLookupCountAndMemory(t *testing.T) {
	r := txn.NewRegistry()
	for i := 0; i < 10_000; i++ {
		id := r.Begin()
		if i >= 1000 {
			r.Commit(id, uint64(i))
		}
	}
	active := make([]txn.ID, 1000)
	for i := range active {
		active[i] = txn.ID(i)
	}
	s, _ := snapshot.New(5000, active, 999, 0)
	d, err := Decide(r, s, Version{Txn: 5000})
	if err != nil || d.Lookups() > 2 {
		t.Fatalf("lookups=%d err=%v", d.Lookups(), err)
	}
	if s.Len() != 1000 {
		t.Fatalf("snapshot items = %d, want 1000 (independent of 10000 txns)", s.Len())
	}
}

func TestRandomCrossCheck(t *testing.T) {
	r := txn.NewRegistry()
	n := 10_000
	committed := []txn.ID{}
	open := []txn.ID{}
	for i := 0; i < n; i++ {
		id := r.Begin()
		if i%5 != 0 {
			r.Commit(id, uint64(i))
			committed = append(committed, id)
		} else {
			open = append(open, id)
		}
	}
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < n; i++ {
		wm := uint64(rnd.Intn(n + 5))
		// 活跃集只含快照创建时仍在进行的事务（真实语义）。
		k := rnd.Intn(60)
		ids := append([]txn.ID(nil), open[:k]...)
		rnd.Shuffle(len(ids), func(a, b int) { ids[a], ids[b] = ids[b], ids[a] })
		s, err := snapshot.New(wm, ids, txn.ID(1<<40), 0)
		if err != nil {
			t.Fatal(err)
		}
		id := txn.ID(rnd.Intn(n))
		d, derr := Decide(r, s, Version{Txn: id})
		nv, nerr := naiveVisible(r, s, id)
		if (derr != nil) != (nerr != nil) || derr == nil && d.Visible != nv {
			t.Fatalf("mismatch id=%d wm=%d: real=%v(%v) naive=%v(%v)", id, wm, d.Visible, derr, nv, nerr)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	r := txn.NewRegistry()
	target := r.Begin()
	r.Commit(target, 3)
	s, _ := snapshot.New(10, nil, 999, 0)
	const g = 64
	var wg sync.WaitGroup
	vis := make([]bool, g)
	lks := make([]int, g)
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := Decide(r, s, Version{Txn: target})
			if err != nil {
				t.Errorf("err: %v", err)
			}
			vis[i], lks[i] = d.Visible, d.Lookups()
		}(i)
	}
	wg.Wait()
	for i := 1; i < g; i++ {
		if vis[i] != vis[0] || lks[i] != lks[0] {
			t.Fatalf("reader %d diverged: %v/%d vs %v/%d", i, vis[i], lks[i], vis[0], lks[0])
		}
	}
}
