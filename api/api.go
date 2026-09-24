package api

import (
	"errors"
	"math/rand"
	"ontology/store"
)

var ErrInvalidLimit = store.ErrInvalidLimit
var ErrEmptyKey = store.ErrEmptyKey
var ErrLogFull = store.ErrLogFull
var ErrSavepoint = store.ErrSavepoint

type KV struct{ st *store.Store }

func New(l int) (*KV, error) {
	s, err := store.New(l)
	if err != nil {
		return nil, err
	}
	return &KV{s}, nil
}
func mustNew(l int) *KV                   { k, _ := New(l); return k }
func (k *KV) Set(key string, v int) error { return k.st.Set(key, v) }
func (k *KV) Get(key string) (int, bool)  { return k.st.Get(key) }
func (k *KV) Savepoint() int              { return k.st.Savepoint() }
func (k *KV) RollbackTo(id int) error     { return k.st.RollbackTo(id) }
func (k *KV) Release(id int) error        { return k.st.Release(id) }
func (k *KV) CheckLocateO1() bool         { return k.st.CheckLocateIndex() }

var keys = []string{"a", "b", "c", "d"}

// ref is the naive model: a savepoint snapshots the whole map; rollback
// restores it (undo) while release only deletes the snapshot.
type ref struct {
	d   map[string]int
	sp  map[int]map[string]int
	nxt int
}

func newRef() *ref { return &ref{d: map[string]int{}, sp: map[int]map[string]int{}} }
func (r *ref) set(k string, v int) error {
	if k == "" {
		return ErrEmptyKey
	}
	r.d[k] = v
	return nil
}
func (r *ref) save() int {
	id := r.nxt
	r.nxt++
	c := map[string]int{}
	for k, v := range r.d {
		c[k] = v
	}
	r.sp[id] = c
	return id
}
func (r *ref) do(id int, undo bool) error {
	c, ok := r.sp[id]
	if !ok {
		return ErrSavepoint
	}
	if undo {
		r.d = c
	}
	for x := range r.sp {
		if x >= id {
			delete(r.sp, x)
		}
	}
	return nil
}
func eq(k *KV, w map[string]int) bool {
	for _, key := range keys {
		gv, gok := k.Get(key)
		wv, wok := w[key]
		if gok != wok || gok && gv != wv {
			return false
		}
	}
	return true
}
func apply(k *KV, r *ref, t byte, key string, v, id int) (bool, error, error) {
	switch t {
	case 'S':
		return true, k.Set(key, v), r.set(key, v)
	case 'P':
		return k.Savepoint() == r.save(), nil, nil
	case 'B':
		return true, k.RollbackTo(id), r.do(id, true)
	default:
		return true, k.Release(id), r.do(id, false)
	}
}

// twelveTrace replays the twelve prescribed ops on both models (6/8/12 verdicts).
func twelveTrace() bool {
	k, r := mustNew(100), newRef()
	tp := "SPSPSXSBSPSB"
	tk := []string{"a", "", "a", "", "b", "", "c", "", "b", "", "c", ""}
	tv := []int{1, 0, 2, 0, 5, 0, 8, 0, 3, 0, 6, 0}
	tg := []int{-1, -1, -1, -1, -1, 1, -1, 0, -1, -1, -1, 1}
	we := map[int]error{11: ErrSavepoint}
	for i := 0; i < len(tp); i++ {
		idOK, ke, me := apply(k, r, tp[i], tk[i], tv[i], tg[i])
		if !idOK || ke != me || ke != we[i] || !eq(k, r.d) {
			return false
		}
	}
	return true
}

// randomReplay drives random arrivals, empty and dead ids; tiny limits tested externally.
func randomReplay(seed int64) bool {
	k, r := mustNew(100000), newRef()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 300; i++ {
		key := keys[rng.Intn(len(keys))]
		if rng.Intn(20) == 0 {
			key = ""
		}
		idOK, ke, me := apply(k, r, "SSSSPBX"[rng.Intn(7)], key, rng.Intn(9), rng.Intn(6))
		if !idOK || ke != me || !eq(k, r.d) {
			return false
		}
	}
	return true
}
func rejections() bool {
	if _, err := New(0); !errors.Is(err, ErrInvalidLimit) {
		return false
	}
	k := mustNew(1)
	if !errors.Is(k.Set("", 1), ErrEmptyKey) || k.Set("a", 1) != nil ||
		!errors.Is(k.Set("b", 2), ErrLogFull) || !errors.Is(k.RollbackTo(99), ErrSavepoint) ||
		!errors.Is(k.Release(99), ErrSavepoint) {
		return false
	}
	id := k.Savepoint()
	if k.Release(id) != nil || !errors.Is(k.Release(id), ErrSavepoint) {
		return false
	}
	return eq(k, map[string]int{"a": 1}) && errors.Is(k.RollbackTo(id+5), ErrSavepoint)
}

// SelfCheck verifies all four invariants on its built-in sequences.
func (k *KV) SelfCheck() bool {
	return twelveTrace() && randomReplay(1) && randomReplay(413) && rejections()
}
