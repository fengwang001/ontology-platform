package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/txn"
)

type k1 struct {
	tx  string
	seq int
}
type kv struct {
	k string
	v int64
}

// ref 是独立按规范实现的参照模型：去重追加日志，末尾折叠成视图。
type ref struct {
	done map[k1]struct{}
	log  []kv
}

func (r *ref) apply(tx string, recs []api.Rec) (n int, err error) {
	if err = txn.Validate(tx, recs); err != nil {
		return 0, err
	}
	for _, rec := range recs {
		id := k1{tx, rec.Seq}
		if _, ok := r.done[id]; !ok {
			r.done[id] = struct{}{}
			r.log = append(r.log, kv{rec.Key, rec.Val})
			n++
		}
	}
	return n, nil
}

func (r *ref) view() map[string]int64 {
	m := map[string]int64{}
	for _, e := range r.log {
		m[e.k] = e.v
	}
	return m
}

func eq(a, b map[string]int64) bool {
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

// TestViewBatchRecompute 钉住 I1：多档种子下正常批/改值重放/三类非法批随机交织，
// added、错误、View 逐步对照独立参照模型；末尾核验 SelfCheck。
func TestViewBatchRecompute(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		rng, a, r := rand.New(rand.NewSource(seed)), api.New(), &ref{done: map[k1]struct{}{}}
		next, seen := map[string]int{}, []k1{}
		for op := 0; op < 200; op++ {
			tx := fmt.Sprintf("t%d", rng.Intn(4))
			var recs []api.Rec
			switch x := rng.Intn(10); {
			case x == 0:
				tx, recs = "", []api.Rec{{Seq: 0, Key: "x", Val: 1}}
			case x == 1:
				recs = []api.Rec{{Seq: 0, Key: "x", Val: 1}, {Seq: 1, Key: "", Val: 2}}
			case x == 2:
				recs = []api.Rec{{Seq: 7, Key: "x", Val: 1}, {Seq: 7, Key: "y", Val: 2}}
			case x < 5:
				if len(seen) == 0 {
					continue
				}
				id := seen[rng.Intn(len(seen))]
				tx, recs = id.tx, []api.Rec{{Seq: id.seq, Key: "zz", Val: -1}}
			default:
				base := next[tx]
				for j := 0; j < 1+rng.Intn(3); j++ {
					recs = append(recs, api.Rec{Seq: base + j, Key: fmt.Sprintf("k%d", rng.Intn(5)), Val: rng.Int63n(1000) - 500})
				}
				next[tx] += len(recs)
			}
			n1, e1 := a.Commit(tx, recs)
			n2, e2 := r.apply(tx, recs)
			if n1 != n2 || !errors.Is(e1, e2) || !eq(a.View(), r.view()) {
				t.Fatalf("seed=%d op%d: %d/%d %v/%v %v vs %v", seed, op, n1, n2, e1, e2, a.View(), r.view())
			}
			if e1 == nil {
				for _, rec := range recs {
					seen = append(seen, k1{tx, rec.Seq})
				}
			}
		}
		if err := a.SelfCheck(); err != nil {
			t.Fatalf("seed=%d SelfCheck: %v", seed, err)
		}
	}
}

// TestRandomReplay 钉住 I3：多档 m 入库后乱序、改值全量重放，恒 added=0、视图不变。
func TestRandomReplay(t *testing.T) {
	for _, m := range []int{100, 1000} {
		rng, a, items := rand.New(rand.NewSource(int64(m))), api.New(), make([]k1, m)
		for i := range items {
			items[i] = k1{fmt.Sprintf("t%d", i%7), i / 7}
			rec := api.Rec{Seq: i / 7, Key: fmt.Sprintf("k%d", i), Val: int64(i)}
			if _, err := a.Commit(items[i].tx, []api.Rec{rec}); err != nil {
				t.Fatal(err)
			}
		}
		want := a.View()
		rng.Shuffle(m, func(i, j int) { items[i], items[j] = items[j], items[i] })
		for _, id := range items {
			n, err := a.Commit(id.tx, []api.Rec{{Seq: id.seq, Key: "zz", Val: -1}})
			if err != nil || n != 0 || !eq(a.View(), want) {
				t.Fatalf("m=%d %v: added=%d err=%v", m, id, n, err)
			}
		}
	}
}
