// Package api 是 as-of 时间旅行读的对外门面，依赖 store。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/store"
)

type Val = store.Val

var (
	ErrEmptyKey        = store.ErrEmptyKey
	ErrNegativeRead    = store.ErrNegativeRead
	ErrCompacted       = store.ErrCompacted
	ErrNegativeCompact = store.ErrNegativeCompact
)

type API struct{ st *store.Store }

func New() *API                                   { return &API{st: store.New()} }
func (a *API) Write(k string, v Val) (int, error) { return a.st.Write(k, v) }
func (a *API) AsOf(s int) (map[string]Val, error) { return a.st.AsOf(s) }
func (a *API) Compact(upto int) error             { return a.st.Compact(upto) }
func (a *API) MaxSeq() int                        { return a.st.MaxSeq() }

type entry struct {
	seq int
	key string
	val Val
}

func naiveReplay(log []entry, s int) map[string]Val {
	view := map[string]Val{}
	for _, e := range log {
		if e.seq <= s {
			view[e.key] = e.val
		}
	}
	return view
}

// SelfCheck 对内置确定性序列核验四条不变量；用全新实例、不改接收者状态，可并发调用。
func (a *API) SelfCheck() error {
	for _, f := range []func() error{checkCanonical, checkRejected, checkRandom} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func checkCanonical() error {
	x := New()
	for _, w := range []struct {
		k string
		v int
	}{{"a", 1}, {"b", 10}, {"a", 2}} {
		if _, err := x.Write(w.k, w.v); err != nil {
			return err
		}
	}
	want := []map[string]Val{{}, {"a": 1}, {"a": 1, "b": 10}, {"a": 2, "b": 10}, {"a": 2, "b": 10}}
	for s, w := range want {
		if got, err := x.AsOf(s); err != nil || !reflect.DeepEqual(got, w) {
			return fmt.Errorf("AsOf(%d)=%v,%v want %v", s, got, err, w)
		}
	}
	if err := x.Compact(2); err != nil {
		return err
	}
	if _, err := x.AsOf(2); !errors.Is(err, ErrCompacted) {
		return fmt.Errorf("AsOf(2) err=%v want ErrCompacted", err)
	}
	for _, s := range []int{3, 4} {
		if got, err := x.AsOf(s); err != nil || !reflect.DeepEqual(got, want[s]) {
			return fmt.Errorf("AsOf(%d)=%v,%v want %v", s, got, err, want[s])
		}
	}
	return nil
}

func checkRejected() error {
	x := New()
	_, eKey := x.Write("", 1)
	_, eRead := x.AsOf(-1)
	eCompact := x.Compact(-1)
	if !errors.Is(eKey, ErrEmptyKey) || !errors.Is(eRead, ErrNegativeRead) || !errors.Is(eCompact, ErrNegativeCompact) {
		return fmt.Errorf("sentinels: %v %v %v", eKey, eRead, eCompact)
	}
	if x.MaxSeq() != 0 {
		return fmt.Errorf("被拒后 MaxSeq=%d want 0", x.MaxSeq())
	}
	if seq, err := x.Write("z", 7); err != nil || seq != 1 {
		return fmt.Errorf("恢复写入 seq=%d err=%v", seq, err)
	}
	if _, err := x.Write("", 9); err == nil || x.MaxSeq() != 1 {
		return fmt.Errorf("被拒写入分配了 Seq, MaxSeq=%d", x.MaxSeq())
	}
	if got, err := x.AsOf(1); err != nil || !reflect.DeepEqual(got, map[string]Val{"z": 7}) {
		return fmt.Errorf("恢复读 %v err=%v", got, err)
	}
	return nil
}

func checkRandom() error {
	x := New()
	var log []entry
	keys := []string{"a", "b", "c", "d"}
	seed := 42
	for i := 1; i <= 300; i++ {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		seq, err := x.Write(keys[seed%4], i)
		if err != nil {
			return err
		}
		log = append(log, entry{seq, keys[seed%4], i})
	}
	top := x.MaxSeq()
	for _, s := range []int{0, 1, top / 3, top / 2, top, top + 5} {
		if got, err := x.AsOf(s); err != nil || !reflect.DeepEqual(got, naiveReplay(log, s)) {
			return fmt.Errorf("replay mismatch s=%d err=%v", s, err)
		}
	}
	upto := top / 2
	before := map[int]map[string]Val{}
	for s := upto + 1; s <= top+3; s++ {
		v, err := x.AsOf(s)
		if err != nil {
			return err
		}
		before[s] = v
	}
	if err := x.Compact(upto); err != nil {
		return err
	}
	for s, w := range before {
		if got, err := x.AsOf(s); err != nil || !reflect.DeepEqual(got, w) {
			return fmt.Errorf("post-compact AsOf(%d)=%v,%v want %v", s, got, err, w)
		}
	}
	for _, s := range []int{0, upto - 1, upto} {
		if _, err := x.AsOf(s); !errors.Is(err, ErrCompacted) {
			return fmt.Errorf("AsOf(%d) err=%v want ErrCompacted", s, err)
		}
	}
	return nil
}
