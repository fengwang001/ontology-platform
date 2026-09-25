package ssi

import (
	"errors"
	"testing"
)

func seed() *Engine { return NewEngine(map[string]string{"x": "10", "y": "10", "z": "5"}) }
func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestReadYourWrites pins invariant 3: a buffered write is read back and is
// never recorded in the read set; other keys still come from the snapshot.
func TestReadYourWrites(t *testing.T) {
	cases := []struct{ k, w, r string }{
		{"z", "99", "99"}, {"x", "7", "7"}, {"new", "v", "v"},
	}
	for _, c := range cases {
		tx := seed().Begin()
		ok(t, tx.Write(c.k, c.w))
		got, err := tx.Read(c.k)
		if err != nil || got != c.r {
			t.Fatalf("Read(%s) after Write = %q,%v want %q,nil", c.k, got, err, c.r)
		}
		if _, rec := tx.reads[c.k]; rec {
			t.Fatalf("own write of %s leaked into read set: %v", c.k, tx.reads)
		}
	}
	tx := seed().Begin()
	tx.Write("z", "99")
	if v, _ := tx.Read("x"); v != "10" {
		t.Fatalf("snapshot read x = %q want 10", v)
	}
}

// TestWriteSkew pins invariant 2: x+y>=0 survives; the later committer in an
// rw-antidependency pair is rolled back. Either order is serializable.
func TestWriteSkew(t *testing.T) {
	run := func(t2First bool) (x, y string, t1err, t2err error) {
		e := seed()
		t1, t2 := e.Begin(), e.Begin()
		t1.Read("x")
		t1.Read("y")
		t2.Read("x")
		t2.Read("y")
		t1.Write("x", "-10")
		t2.Write("y", "-10")
		if t2First {
			t2err, t1err = t2.Commit(), t1.Commit()
		} else {
			t1err, t2err = t1.Commit(), t2.Commit()
		}
		c := e.Committed()
		return c["x"], c["y"], t1err, t2err
	}
	if x, y, t1e, t2e := run(false); t1e != nil || t2e != ErrConflict || x != "-10" || y != "10" {
		t.Fatalf("T1->T2: x=%s y=%s errs=%v,%v want -10,10 nil,conflict", x, y, t1e, t2e)
	}
	if x, y, t1e, t2e := run(true); t2e != nil || t1e != ErrConflict || x != "10" || y != "-10" {
		t.Fatalf("T2->T1: x=%s y=%s errs=%v,%v want 10,-10 conflict,nil", x, y, t1e, t2e)
	}
}

// TestRejectedOpsNoTrace pins invariant 4: three distinct sentinels, rejected
// ops mutate nothing, and the engine stays usable.
func TestRejectedOpsNoTrace(t *testing.T) {
	var nt *Txn
	for _, op := range []func() error{
		func() error { _, e := nt.Read("k"); return e },
		func() error { return nt.Write("k", "v") },
		nt.Commit,
	} {
		if err := op(); !errors.Is(err, ErrNoTxn) {
			t.Fatalf("nil-txn op err=%v want ErrNoTxn", err)
		}
	}
	e := NewEngine(nil)
	tx := e.Begin()
	if _, err := tx.Read(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty Read err=%v want ErrEmptyKey", err)
	}
	if err := tx.Write("", "v"); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty Write err=%v want ErrEmptyKey", err)
	}
	if len(tx.reads) != 0 || len(tx.writes) != 0 || len(e.Committed()) != 0 {
		t.Fatalf("rejected empty-key ops left a trace: %v %v %v", tx.reads, tx.writes, e.Committed())
	}
	ok(t, tx.Write("k", "v"))
	ok(t, tx.Commit())
	for _, op := range []func() error{
		func() error { _, e := tx.Read("k"); return e },
		tx.Commit,
		func() error { return tx.Write("k", "w") },
	} {
		if err := op(); !errors.Is(err, ErrTxnFinished) {
			t.Fatalf("post-commit op err=%v want ErrTxnFinished", err)
		}
	}
	if c := e.Committed(); len(c) != 1 || c["k"] != "v" {
		t.Fatalf("post-commit ops mutated state: %v", c)
	}
	ok(t, e.Begin().Commit()) // engine still usable
}

// TestCommitCheckIndex pins the complexity bound: the unexported examined
// count is a constant independent of m committed readers of the same key.
func TestCommitCheckIndex(t *testing.T) {
	prev := -1
	for _, m := range []int{100, 1000, 10000} {
		e := NewEngine(map[string]string{"k": "1"})
		for j := 0; j < m; j++ {
			r := e.Begin()
			r.Read("k")
			ok(t, r.Commit())
		}
		w := e.Begin()
		ok(t, w.Write("k", "2"))
		ok(t, w.Commit())
		if e.lastChecked > 1 || (prev >= 0 && e.lastChecked != prev) {
			t.Fatalf("m=%d examined=%d, want constant independent of m (prev %d)", m, e.lastChecked, prev)
		}
		if e.Committed()["k"] != "2" {
			t.Fatalf("writer value not applied")
		}
		prev = e.lastChecked
	}
}
