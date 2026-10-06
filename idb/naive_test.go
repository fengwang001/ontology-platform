package idb_test

import (
	"math/rand"
	"testing"

	"ontology/idb"
)

// naiveModel 是独立编写的顺序化朴素参考模型：
// 已提交数据 committed（store -> key -> value）；
// RW 事务用暂存区，提交时整体应用，中止时整体丢弃，期间读到自己的写；
// RO 事务在开始那一刻复制快照，期间只读快照。
type naiveModel struct {
	committed map[string]map[string]string
}

type naiveTx struct {
	mode   idb.Mode
	stores []string
	work   map[string]map[string]*string // nil 指针表示删除
	snap   map[string]map[string]string
	ended  bool
	model  *naiveModel
}

func newNaive() *naiveModel {
	return &naiveModel{committed: map[string]map[string]string{}}
}

func (m *naiveModel) ensureStore(s string) {
	if _, ok := m.committed[s]; !ok {
		m.committed[s] = map[string]string{}
	}
}

func (m *naiveModel) begin(mode idb.Mode, stores []string) *naiveTx {
	t := &naiveTx{mode: mode, stores: stores, model: m}
	if mode == idb.ReadOnly {
		t.snap = map[string]map[string]string{}
		for _, s := range stores {
			t.snap[s] = map[string]string{}
			for k, v := range m.committed[s] {
				t.snap[s][k] = v
			}
		}
	} else {
		t.work = map[string]map[string]*string{}
		for _, s := range stores {
			t.work[s] = map[string]*string{}
		}
	}
	return t
}

func (t *naiveTx) inScope(s string) bool {
	for _, x := range t.stores {
		if x == s {
			return true
		}
	}
	return false
}

func (t *naiveTx) get(s, k string) (string, bool) {
	if t.mode == idb.ReadOnly {
		v, ok := t.snap[s][k]
		return v, ok
	}
	if w, ok := t.work[s][k]; ok {
		if w == nil {
			return "", false
		}
		return *w, true
	}
	v, ok := t.model.committed[s][k]
	return v, ok
}

func (t *naiveTx) put(s, k, v string, addOnly bool) *idb.Error {
	if _, ok := t.get(s, k); addOnly && ok {
		return idb.KindConstraint.New("conflict")
	}
	vv := v
	t.work[s][k] = &vv
	return nil
}

func (t *naiveTx) del(s, k string) {
	t.work[s][k] = nil
}

func (t *naiveTx) commit() {
	for s, ws := range t.work {
		for k, v := range ws {
			if v == nil {
				delete(t.model.committed[s], k)
			} else {
				t.model.committed[s][k] = *v
			}
		}
	}
	t.ended = true
}

func (t *naiveTx) abort() { t.ended = true }

// op 是随机序列中的一步。
type op struct {
	kind  int // 0 get 1 put 2 add 3 del
	store int // 作用域内下标
	key   string
	value string
	abort bool // 事务末尾是否中止
}

const (
	oGet = iota
	oPut
	oAdd
	oDel
)

func TestDifferentialAgainstNaiveModel(t *testing.T) {
	const iterations = 400
	const maxOps = 12
	const storesN = 3

	rng := rand.New(rand.NewSource(20261006))
	storeNames := func() []string {
		ns := []string{"s0", "s1", "s2"}
		return ns
	}

	for iter := 0; iter < iterations; iter++ {
		seed := rng.Int63()
		r := rand.New(rand.NewSource(seed))

		nOps := 1 + r.Intn(maxOps)
		mode := idb.ReadWrite
		// 每个序列一个事务；约 1/4 用只读序列。
		readOnly := r.Intn(4) == 0
		if readOnly {
			mode = idb.ReadOnly
		}
		scope := storeNames()
		ops := make([]op, nOps)
		for i := range ops {
			o := op{
				kind:  r.Intn(4),
				store: r.Intn(storesN),
				key:   "k" + string(rune('0'+r.Intn(5))),
				value: "v",
			}
			if readOnly {
				o.kind = oGet
			}
			ops[i] = o
		}
		willAbort := !readOnly && r.Intn(2) == 0

		// ---- 朴素模型 ----
		nm := newNaive()
		for _, s := range scope {
			nm.ensureStore(s)
		}
		// 预置已提交键（与真实内核种子一致）。
		seedTx := nm.begin(idb.ReadWrite, scope)
		for si := 0; si < storesN; si++ {
			v0 := "seed"
			seedTx.work[scope[si]]["k0"] = &v0
		}
		v1 := "seed1"
		seedTx.work[scope[0]]["k1"] = &v1
		seedTx.commit()

		ntx := nm.begin(mode, scope)
		type naiveRead struct {
			v  string
			ok bool
		}
		var naiveReads []naiveRead
		var naiveErrs []idb.ErrKind
		for _, o := range ops {
			s := scope[o.store]
			switch o.kind {
			case oGet:
				v, ok := ntx.get(s, o.key)
				naiveReads = append(naiveReads, naiveRead{v, ok})
			case oPut:
				if e := ntx.put(s, o.key, o.value, false); e != nil {
					naiveErrs = append(naiveErrs, e.Kind)
				}
			case oAdd:
				if e := ntx.put(s, o.key, o.value, true); e != nil {
					naiveErrs = append(naiveErrs, e.Kind)
				}
			case oDel:
				ntx.del(s, o.key)
			}
		}
		if willAbort {
			ntx.abort()
		} else {
			ntx.commit()
		}

		// ---- 真实内核 ----
		k := testKernel(t)
		c := upgrade(t, k, "db", 1, mkStores(scope...))
		rwTx(t, c, scope, func(tx *idb.Transaction) {
			for si := 0; si < storesN; si++ {
				putKV(t, tx, scope[si], "k0", "seed", false)
			}
			putKV(t, tx, scope[0], "k1", "seed1", false)
		})

		var realReads []naiveRead
		var realErrs []idb.ErrKind
		ferr := idb.WithTx(c, mode, scope, func(tx *idb.Transaction) error {
			for _, o := range ops {
				if tx.State() != idb.Running {
					break
				}
				s := scope[o.store]
				switch o.kind {
				case oGet:
					v, ok, e := idb.SyncGet(tx, s, o.key)
					if e != nil {
						if errIsKind(e, idb.KindCanceled) || errIsKind(e, idb.KindTxInactive) {
							return errAbortedSentinel
						}
						t.Fatalf("seed=%d get: %v", seed, e)
					} else {
						realReads = append(realReads, naiveRead{v, ok})
					}
				case oPut:
					if e := idb.SyncPut(tx, s, o.key, o.value, false); e != nil {
						if errIsKind(e, idb.KindCanceled) || errIsKind(e, idb.KindTxInactive) {
							return errAbortedSentinel
						}
						realErrs = append(realErrs, kindOf(e))
					}
				case oAdd:
					r, e := tx.PutEx(s, []byte(o.key), []byte(o.value), true,
						idb.WithIgnorable(),
						idb.WithOnError(func(err *idb.Error) {
							realErrs = append(realErrs, err.Kind)
						}),
						idb.WithOnSuccess(func(*idb.RequestResult) {}))
					if e != nil {
						t.Fatalf("seed=%d add issue: %v", seed, e)
					}
					_ = r
					if !waitIdle(t, tx, s) {
						return errAbortedSentinel
					}
				case oDel:
					if e := idb.SyncDelete(tx, s, o.key); e != nil {
						if errIsKind(e, idb.KindCanceled) || errIsKind(e, idb.KindTxInactive) {
							return errAbortedSentinel
						}
						t.Fatalf("seed=%d del: %v", seed, e)
					}
				}
			}
			if willAbort {
				if e := tx.Abort(); e != nil {
					if !errIsKind(e, idb.KindTxInactive) {
						t.Fatal(e)
					}
				}
				return errAbortedSentinel
			}
			if tx.State() != idb.Running {
				return errAbortedSentinel
			}
			return nil
		})
		if willAbort {
			if ferr == nil || !errIsKind(ferr, idb.KindCanceled) {
				t.Fatalf("seed=%d want aborted tx, got %v", seed, ferr)
			}
		} else if ferr != nil {
			t.Fatalf("seed=%d tx: %v", seed, ferr)
		}

		// 比较读结果序列。
		if len(realReads) != len(naiveReads) {
			t.Fatalf("seed=%d reads %d vs %d", seed, len(realReads), len(naiveReads))
		}
		for i := range realReads {
			if realReads[i] != naiveReads[i] {
				t.Fatalf("seed=%d read[%d] real=%+v naive=%+v",
					seed, i, realReads[i], naiveReads[i])
			}
		}
		if len(realErrs) != len(naiveErrs) {
			t.Fatalf("seed=%d errs %v vs %v", seed, realErrs, naiveErrs)
		}
		for i := range realErrs {
			if realErrs[i] != naiveErrs[i] {
				t.Fatalf("seed=%d err[%d] real=%v naive=%v",
					seed, i, realErrs[i], naiveErrs[i])
			}
		}

		// 比较最终已提交数据（含中止回滚）。
		for _, s := range scope {
			real := dumpStore(t, k, "db", s)
			if len(real) != len(nm.committed[s]) {
				t.Fatalf("seed=%d store %s real=%v naive=%v",
					seed, s, real, nm.committed[s])
			}
			for key, want := range nm.committed[s] {
				got, ok := real[key]
				if !ok || got != want {
					t.Fatalf("seed=%d store %s key %s real=%q(%v) naive=%q",
						seed, s, key, got, ok, want)
				}
			}
		}
	}
}

func dumpStore(t *testing.T, k *idb.Kernel, db, store string) map[string]string {
	t.Helper()
	c, err := k.Open(db, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	err = idb.WithTx(c, idb.ReadOnly, []string{store}, func(tx *idb.Transaction) error {
		for i := 0; i < 256; i++ {
			key := "k" + string(rune(i))
			v, ok, e := idb.SyncGet(tx, store, key)
			if e != nil {
				t.Fatal(e)
			}
			if ok {
				out[key] = v
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func kindOf(err error) idb.ErrKind {
	if e, ok := err.(*idb.Error); ok {
		return e.Kind
	}
	return -1
}

var errAbortedSentinel = idb.KindCanceled.New("planned abort")

// waitIdle 用一个 FIFO 屏障请求排空前序投递；返回事务是否仍然存活。
func waitIdle(t *testing.T, tx *idb.Transaction, scopeStore string) bool {
	t.Helper()
	alive := true
	done := make(chan struct{}, 1)
	r, err := tx.GetEx(scopeStore, []byte("__barrier__"),
		idb.WithOnSuccess(func(*idb.RequestResult) { done <- struct{}{} }),
		idb.WithOnError(func(*idb.Error) { alive = false; done <- struct{}{} }),
	)
	if err != nil {
		return false
	}
	_ = r
	<-done
	return alive
}
