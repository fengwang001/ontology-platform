package portdb

import (
	"errors"
	"testing"

	"ontology/numplan"
)

func newFixture(t *testing.T, lmin, q int64) (*DB, string) {
	t.Helper()
	p := numplan.New()
	if err := p.AssignBlock("1380", 11, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.AssignBlock("13805", 11, 2, 10); err != nil {
		t.Fatal(err)
	}
	d := New(lmin, q)
	d.SetPlan(p)
	return d, "13805001234"
}

func TestRejectionOrder(t *testing.T) {
	const n = "13805001234"
	cases := []struct {
		name string
		call func(d *DB) error
		want error
	}{
		{"1 invalid arg", func(d *DB) error {
			_, e := d.RequestPort("12", 2, 3, 100, 20)
			return e
		}, ErrInvalidArgument},
		{"2 clock rewind", func(d *DB) error {
			_, e := d.RequestPort(n, 2, 3, 100, 30)
			if e != nil {
				t.Fatalf("setup failed: %v", e)
			}
			_, e = d.RequestPort(n, 3, 4, 100, 25)
			return e
		}, ErrClockRewound},
		{"3 unallocated", func(d *DB) error {
			_, e := d.RequestPort("19900000000", 2, 3, 100, 20)
			return e
		}, ErrNumberUnallocated},
		{"5 pending exists", func(d *DB) error {
			if _, e := d.RequestPort(n, 2, 3, 100, 20); e != nil {
				t.Fatal(e)
			}
			_, e := d.RequestPort(n, 2, 4, 110, 21)
			return e
		}, ErrPendingExists},
		{"6 donor mismatch", func(d *DB) error {
			_, e := d.RequestPort(n, 9, 3, 100, 20)
			return e
		}, ErrDonorMismatch},
		{"7 same operator", func(d *DB) error {
			_, e := d.RequestPort(n, 2, 2, 100, 20)
			return e
		}, ErrSameOperator},
		{"8 lead time short", func(d *DB) error {
			_, e := d.RequestPort(n, 2, 3, 69, 20)
			return e
		}, ErrLeadTime},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, _ := newFixture(t, 50, 90)
			if err := c.call(d); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v basis=%s", err, c.want, c.name)
			}
			wantClock := int64(0)
			if c.name == "5 pending exists" || c.name == "2 clock rewind" {
				wantClock = 20
				if c.name == "2 clock rewind" {
					wantClock = 30
				}
			}
			if d.MaxNow() != wantClock {
				t.Fatalf("%s: rejection changed clock: %d", c.name, d.MaxNow())
			}
		})
	}
}

func TestLeadTimeExactAndEffectiveAt(t *testing.T) {
	d, n := newFixture(t, 50, 90)
	if _, err := d.RequestPort(n, 2, 3, 20, 20); !errors.Is(err, ErrLeadTime) {
		t.Fatalf("at==now with Lmin=50: %v", err)
	}
	id, err := d.RequestPort(n, 2, 3, 70, 20)
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("order id = %d want 1 (rejected must not consume ids)", id)
	}
	if s, _, f, e := d.LookupAt(n, 69); e != nil || f || s != 2 {
		t.Fatalf("t=69 server=%d frozen=%v err=%v want 2", s, f, e)
	}
	if s, _, f, e := d.LookupAt(n, 70); e != nil || f || s != 3 {
		t.Fatalf("t=70 server=%d frozen=%v err=%v want 3 (exact at effective)", s, f, e)
	}
	// Cancel boundary: now < at only.
	if err := d.Cancel(1, 70); !errors.Is(err, ErrAlreadyEffective) {
		t.Fatalf("cancel at 70 = %v", err)
	}
}

func TestCancelBeforeEffective(t *testing.T) {
	d, n := newFixture(t, 50, 90)
	id, _ := d.RequestPort(n, 2, 3, 100, 20)
	if err := d.Cancel(id, 99); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(id, 99); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("second cancel = %v", err)
	}
	if s, _, _, _ := d.LookupAt(n, 200); s != 2 {
		t.Fatalf("cancelled order took effect: server=%d", s)
	}
	if err := d.Cancel(999, 200); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("unknown order = %v", err)
	}
}

func TestFreezeWindowAndAutoCancel(t *testing.T) {
	d, n := newFixture(t, 50, 90)
	id, _ := d.RequestPort(n, 2, 3, 100, 20)
	if err := d.Disconnect(n, 40); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(id, 41); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("auto-cancelled order = %v", err)
	}
	// [40,130) frozen.
	for _, tc := range []struct {
		t      int64
		frozen bool
	}{
		{39, false},
		{40, true},
		{129, true},
		{130, false},
	} {
		_, _, f, err := d.LookupAt(n, tc.t)
		if err != nil || f != tc.frozen {
			t.Fatalf("t=%d frozen=%v err=%v want %v (half-open [40,130))", tc.t, f, err, tc.frozen)
		}
	}
	if s, h, _, err := d.LookupAt(n, 130); err != nil || s != 2 || h != 2 {
		t.Fatalf("after thaw s=%d h=%d err=%v want 2,2", s, h, err)
	}
	if _, err := d.RequestPort(n, 2, 3, 200, 100); !errors.Is(err, ErrFrozen) {
		t.Fatalf("request during freeze = %v", err)
	}
	if err := d.Disconnect(n, 100); !errors.Is(err, ErrFrozen) {
		t.Fatalf("second disconnect = %v", err)
	}
	if err := d.Disconnect("19900000000", 100); !errors.Is(err, ErrNumberUnallocated) {
		t.Fatalf("disconnect unallocated = %v", err)
	}
	// After thaw a new port is possible.
	if _, err := d.RequestPort(n, 2, 3, 200, 130); err != nil {
		t.Fatalf("port after thaw: %v", err)
	}
}

func TestHistoryImmutability(t *testing.T) {
	d, n := newFixture(t, 50, 90)
	d.RequestPort(n, 2, 3, 100, 20)
	s1, _, f1, _ := d.LookupAt(n, 99)
	s2, _, f2, _ := d.LookupAt(n, 100)
	d.Disconnect(n, 150)
	s3, _, f3, _ := d.LookupAt(n, 99)
	s4, _, f4, _ := d.LookupAt(n, 100)
	if s1 != 2 || f1 || s3 != 2 || f3 {
		t.Fatalf("pre-port history rewritten: %d/%v %d/%v", s1, f1, s3, f3)
	}
	if s2 != 3 || f2 || s4 != 3 || f4 {
		t.Fatalf("effective history rewritten by disconnect: %d/%v %d/%v", s2, f2, s4, f4)
	}
}

func TestOrderIDsContiguousAfterReject(t *testing.T) {
	d, n := newFixture(t, 0, 0)
	if _, err := d.RequestPort(n, 2, 2, 0, 20); !errors.Is(err, ErrSameOperator) {
		t.Fatal(err)
	}
	id1, _ := d.RequestPort(n, 2, 3, 20, 20)
	id2, _ := d.RequestPort(n, 3, 1, 40, 30)
	if id1 != 1 || id2 != 2 {
		t.Fatalf("ids = %d,%d want 1,2", id1, id2)
	}
}
