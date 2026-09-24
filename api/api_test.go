package api

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/mvcc"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestBatchReferenceEquivalence(t *testing.T) { // invariant 1
	for iter := 0; iter < 50; iter++ {
		d, rng := New(), rand.New(rand.NewSource(int64(iter)))
		ref, pend, live := map[string]string{}, map[int]map[string]string{}, []int{}
		for op := 0; op < 200; op++ {
			switch rng.Intn(10) {
			case 0:
				id := d.Begin()
				pend[id], live = map[string]string{}, append(live, id)
			case 1, 2, 3:
				if len(live) == 0 {
					continue
				}
				id := live[rng.Intn(len(live))]
				k, v := fmt.Sprintf("k%d", rng.Intn(5)), fmt.Sprintf("%d-%d", iter, op)
				must(t, d.Write(id, k, v))
				pend[id][k] = v
			case 4:
				if len(live) == 0 {
					continue
				}
				i := rng.Intn(len(live))
				id := live[i]
				must(t, d.Commit(id))
				for k, v := range pend[id] {
					ref[k] = v
				}
				live = append(live[:i], live[i+1:]...)
			default:
				k := fmt.Sprintf("k%d", rng.Intn(6)) // k5 is never written
				if rng.Intn(2) == 0 {
					g, gok := d.Read(k)
					w, wok := ref[k]
					if g != w || gok != wok {
						t.Fatalf("op%d Read(%s)=%q,%v want %q,%v", op, k, g, gok, w, wok)
					}
				} else if len(live) > 0 {
					id := live[rng.Intn(len(live))]
					g, gok, err := d.ReadTx(id, k)
					must(t, err)
					w, wok := pend[id][k]
					if !wok {
						w, wok = ref[k]
					}
					if g != w || gok != wok {
						t.Fatalf("op%d ReadTx(%s)=%q,%v want %q,%v", op, k, g, gok, w, wok)
					}
				}
			}
		}
	}
}
func TestUncommittedInvisible(t *testing.T) { // invariant 2
	for n := 1; n <= 8; n++ {
		d, ids := New(), make([]int, n)
		for i := range ids {
			ids[i] = d.Begin()
			must(t, d.Write(ids[i], "shared", fmt.Sprintf("v%d", i)))
			must(t, d.Write(ids[i], fmt.Sprintf("k%d", i), "x"))
		}
		if _, ok := d.Read("shared"); ok {
			t.Fatalf("n=%d dirty Read", n)
		}
		witness := d.Begin()
		for i := 0; i <= n; i++ {
			if _, ok, err := d.ReadTx(witness, fmt.Sprintf("k%d", i)); err != nil || ok {
				t.Fatalf("n=%d dirty ReadTx k%d", n, i)
			}
		}
	}
}
func TestOwnWriteVisible(t *testing.T) { // invariant 3
	d := New()
	base := d.Begin()
	must(t, d.Write(base, "a", "A0"))
	must(t, d.Commit(base))
	tx := d.Begin()
	if v, ok, _ := d.ReadTx(tx, "a"); !ok || v != "A0" {
		t.Fatalf("committed fallback=%q,%v", v, ok)
	}
	must(t, d.Write(tx, "a", "A1"))
	must(t, d.Write(tx, "b", "B1"))
	must(t, d.Write(tx, "a", "A2")) // overwrite
	for _, c := range []struct{ k, w string }{{"a", "A2"}, {"b", "B1"}} {
		if v, ok, _ := d.ReadTx(tx, c.k); !ok || v != c.w {
			t.Fatalf("ReadTx(%s)=%q,%v want %s", c.k, v, ok, c.w)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) { // invariant 4
	d := New()
	base := d.Begin()
	must(t, d.Write(base, "K", "1"))
	must(t, d.Commit(base))
	live := d.Begin()
	cases := []struct {
		err  error
		want error
	}{
		{d.Write(live, "", "v"), mvcc.ErrEmptyKey},
		{d.Write(live, "K", ""), mvcc.ErrEmptyValue},
		{d.Write(1<<30, "K", "z"), mvcc.ErrTxNotBegun},
		{d.Commit(1 << 30), mvcc.ErrTxNotBegun},
		{func() error { _, _, e := d.ReadTx(1<<30, "K"); return e }(), mvcc.ErrTxNotBegun},
		{d.Write(base, "K", "2"), mvcc.ErrTxCommitted},
		{d.Commit(base), mvcc.ErrTxCommitted},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("case %d err=%v want %v", i, c.err, c.want)
		}
	}
	if v, ok, _ := d.ReadTx(live, "K"); !ok || v != "1" { // no pending shadow
		t.Fatalf("pending corrupted: %q,%v", v, ok)
	}
	if v, ok := d.Read("K"); !ok || v != "1" {
		t.Fatalf("committed K=%q,%v", v, ok)
	}
	must(t, d.Write(live, "K", "2"))
	must(t, d.Commit(live))
	if v, ok := d.Read("K"); !ok || v != "2" { // store still usable
		t.Fatalf("unusable after rejects: %q,%v", v, ok)
	}
}
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
