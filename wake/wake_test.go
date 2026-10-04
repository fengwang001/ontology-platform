package wake

import "testing"

func TestWindows(t *testing.T) {
	cases := []struct {
		name string
		p    Params
		from int64
		want int64
	}{
		{"before offset", Params{100, 10, 5}, 0, 10},
		{"at offset", Params{100, 10, 5}, 10, 10},
		{"inside window -> next start is after", Params{100, 10, 5}, 14, 110},
		{"at window end excluded", Params{100, 10, 5}, 15, 110},
		{"gap", Params{100, 10, 5}, 50, 110},
		{"second window", Params{100, 10, 5}, 112, 210},
		{"tight window", Params{20, 0, 20}, 35, 40},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FirstStart(c.p, c.from); got != c.want {
				t.Fatalf("FirstStart(%v,%d)=%d want %d", c.p, c.from, got, c.want)
			}
		})
	}
}

func TestWindowStart(t *testing.T) {
	d := New(Params{100, 10, 5})
	probe := []struct {
		now  int64
		s    int64
		in   bool
		next int64
	}{
		{0, 0, false, 10},
		{10, 10, true, 10},
		{14, 10, true, 14},
		{15, 0, false, 110},
		{110, 110, true, 110},
	}
	for _, c := range probe {
		s, in := d.WindowStart(c.now)
		if in != c.in || (in && s != c.s) {
			t.Fatalf("WindowStart(%d)=(%d,%v) want (%d,%v)", c.now, s, in, c.s, c.in)
		}
		n := d.NextAvailable(c.now)
		if n != c.next {
			t.Fatalf("NextAvailable(%d)=%d want %d", c.now, n, c.next)
		}
	}
}

type point struct {
	now int64
	s   int64
	in  bool
}

// 换参三种落点 + 过渡期满后生效。
func TestReconfigure(t *testing.T) {
	cases := []struct {
		name   string
		reconf int64
		newP   Params
		wantE  int64
		probe  []point
	}{
		{
			"inside old window -> old window runs out",
			12, Params{40, 0, 10}, 15,
			[]point{{13, 10, true}, {14, 10, true}, {15, 0, false}, {39, 0, false}, {40, 40, true}, {80, 80, true}},
		},
		{
			"in gap -> immediate e=now",
			20, Params{40, 0, 10}, 20,
			[]point{{20, 0, false}, {39, 0, false}, {40, 40, true}},
		},
		{
			"new window starts exactly at e",
			20, Params{20, 0, 10}, 20,
			[]point{{20, 20, true}, {29, 20, true}, {30, 0, false}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := New(Params{100, 10, 5})
			if e := d.Reconfigure(c.newP, c.reconf); e != c.wantE {
				t.Fatalf("e=%d want %d", e, c.wantE)
			}
			for _, p := range c.probe {
				s, in := d.WindowStart(p.now)
				if in != p.in || (in && s != p.s) {
					t.Fatalf("after reconf WindowStart(%d)=(%d,%v) want (%d,%v)", p.now, s, in, p.s, p.in)
				}
			}
		})
	}
}

// 过渡期内再次换参以当前仍生效的旧参数为基准。
func TestReconfigureDuringGrace(t *testing.T) {
	d := New(Params{100, 10, 5})
	if e := d.Reconfigure(Params{40, 0, 10}, 12); e != 15 {
		t.Fatalf("e1=%d want 15", e)
	}
	if e := d.Reconfigure(Params{30, 0, 8}, 13); e != 15 {
		t.Fatalf("e2=%d want 15", e)
	}
	if _, in := d.WindowStart(14); !in {
		t.Fatalf("now=14 should still be in old window")
	}
	if s, in := d.WindowStart(30); !in || s != 30 {
		t.Fatalf("now=30 should be in new window starting 30, got (%d,%v)", s, in)
	}
}
