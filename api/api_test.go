package api

import (
	"errors"
	"testing"
)

// TestPartialRollback pins invariant 1 via the twelve-step table.
func TestPartialRollback(t *testing.T) {
	c, _ := New(100)
	for _, o := range twelve {
		var err error
		switch o.kind {
		case 'S':
			err = c.Set(o.key, o.val)
		case 'P':
			if id := c.Savepoint(); id != o.id {
				t.Fatalf("id=%d want %d", id, o.id)
			}
		case 'R':
			err = c.RollbackTo(o.id)
		case 'X':
			err = c.Release(o.id)
		}
		if !errors.Is(err, o.wantErr) || !eqMap(dump(c), o.want) {
			t.Fatalf("step %+v: err=%v store=%v", o, err, dump(c))
		}
	}
}

// TestNaiveEquivalence pins invariant 2 (random sequences vs naive model).
func TestNaiveEquivalence(t *testing.T) {
	if err := CheckNaive(); err != nil {
		t.Fatal(err)
	}
}

// TestReleaseSemantics pins invariant 3.
func TestReleaseSemantics(t *testing.T) {
	if err := ReleaseScenario(); err != nil {
		t.Fatal(err)
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4 and the four distinct errors.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("New(0): %v", err)
	}
	s := []error{ErrInvalidLimit, ErrEmptyKey, ErrLogFull, ErrSavepointNotFound}
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if errors.Is(s[i], s[j]) {
				t.Fatalf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
	type tc struct {
		name string
		lim  int
		prep func(*KV)
		op   func(*KV) error
		free func(*KV)
		want map[string]int
		err  error
	}
	cases := []tc{
		{"empty-key", 10, nil, func(k *KV) error { return k.Set("", 1) }, nil, map[string]int{}, ErrEmptyKey},
		{"log-full", 2,
			func(k *KV) { _ = k.Savepoint(); _ = k.Set("a", 1); _ = k.Set("b", 2) },
			func(k *KV) error { return k.Set("c", 3) },
			func(k *KV) { _ = k.RollbackTo(0) }, map[string]int{"a": 1, "b": 2}, ErrLogFull},
		{"rollback-missing", 10, nil, func(k *KV) error { return k.RollbackTo(5) }, nil, map[string]int{}, ErrSavepointNotFound},
		{"release-missing", 10, nil, func(k *KV) error { return k.Release(5) }, nil, map[string]int{}, ErrSavepointNotFound},
		{"consumed", 10, func(k *KV) { _ = k.RollbackTo(k.Savepoint()) },
			func(k *KV) error { return k.RollbackTo(0) }, nil, map[string]int{}, ErrSavepointNotFound},
	}
	for _, c := range cases {
		k, _ := New(c.lim)
		if c.prep != nil {
			c.prep(k)
		}
		before := dump(k)
		err := c.op(k)
		if !errors.Is(err, c.err) || !eqMap(dump(k), before) || !eqMap(dump(k), c.want) {
			t.Fatalf("%s: err=%v store=%v want=%v", c.name, err, dump(k), c.want)
		}
		if c.free != nil {
			c.free(k)
		}
		if e := k.Set("z", 0); e != nil { // store still usable after rejection
			t.Fatalf("%s: unusable after rejection: %v", c.name, e)
		}
	}
}

// TestLocateO1 proves index-based location at several m scales (bool only).
func TestLocateO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		if err := LocateScenario(m); err != nil {
			t.Fatal(err)
		}
	}
}

// TestConcurrentGet: N goroutines read the same keys and must agree keywise.
func TestConcurrentGet(t *testing.T) {
	if err := ConcurrentScenario(16); err != nil {
		t.Fatal(err)
	}
}

func TestSelfCheck(t *testing.T) {
	k, _ := New(1000)
	if err := k.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
