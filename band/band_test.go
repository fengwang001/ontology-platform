package band

import "testing"

func TestNewLimitBands(t *testing.T) {
	cases := []struct {
		name    string
		prev, l int64
		up, dn  int64
	}{
		{"整百无舍入", 1000, 1000, 1100, 900},
		{"上下都向内取整", 1005, 1000, 1105, 905},
		{"上限舍去下限进一", 1003, 1000, 1103, 903},
		{"上限恰整", 1000, 2000, 1200, 800},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(Params{Prev: c.prev, L: c.l})
			if s.Up() != c.up || s.Dn() != c.dn {
				t.Fatalf("up/dn = %d/%d, want %d/%d", s.Up(), s.Dn(), c.up, c.dn)
			}
		})
	}
}

func TestInLimitEndpoints(t *testing.T) {
	s := New(Params{Prev: 1005, L: 1000})
	for _, p := range []int64{904, 905, 1000, 1105, 1106} {
		got := s.InLimit(p)
		want := p >= 905 && p <= 1105
		if got != want {
			t.Fatalf("InLimit(%d)=%v want %v", p, got, want)
		}
	}
}

func TestOverEquality(t *testing.T) {
	// R=1000,D=200bps：阈值 ±20，取等不超带，多 1 超带。
	cases := []struct {
		x, r, d int64
		want    bool
		note    string
	}{
		{1020, 1000, 200, false, "动态带上沿取等"},
		{980, 1000, 200, false, "动态带下沿取等"},
		{1021, 1000, 200, true, "动态带多 1"},
		{979, 1000, 200, true, "动态带少 1"},
		{1050, 1000, 500, false, "静态带上沿取等"},
		{1051, 1000, 500, true, "静态带多 1"},
		{950, 1000, 500, false, "静态带下沿取等"},
		{949, 1000, 500, true, "静态带少 1"},
	}
	for _, c := range cases {
		if got := Over(c.x, c.r, c.d); got != c.want {
			t.Fatalf("%s: Over(%d,%d,%d)=%v want %v", c.note, c.x, c.r, c.d, got, c.want)
		}
	}
}

func TestBookRd(t *testing.T) {
	b := NewBook(1000)
	// 无合格成交时回落到 Rs。
	if rd, _ := b.Rd(100, 10); rd != 1000 {
		t.Fatalf("空簿 Rd=%d want 1000", rd)
	}
	b.Append(Tick{Time: 1, Price: 1010})
	b.Append(Tick{Time: 5, Price: 1020})
	b.Append(Tick{Time: 11, Price: 1031})
	// now-W=1：时刻恰为 1 的成交算在内，Rd=1010。
	if rd, n := b.Rd(11, 10); rd != 1010 || n != 1 {
		t.Fatalf("Rd(11)=%d,pop=%d want 1010,1", rd, n)
	}
	// now-W=5：老化到时刻 5，Rd=1020。
	if rd, n := b.Rd(15, 10); rd != 1020 || n != 1 {
		t.Fatalf("Rd(15)=%d,pop=%d want 1020,1", rd, n)
	}
	// now-W=20：全部老化，Rd 为最后一笔 1031，簿清空。
	if rd, n := b.Rd(30, 10); rd != 1031 || n != 1 {
		t.Fatalf("Rd(30)=%d,pop=%d want 1031,1", rd, n)
	}
	if b.Len() != 0 || b.Popped() != 3 {
		t.Fatalf("Len=%d popped=%d want 0,3", b.Len(), b.Popped())
	}
	// 无新成交时 Rd 停在最近老化价，不回落到 Rs。
	if rd, _ := b.Rd(40, 10); rd != 1031 {
		t.Fatalf("Rd(40)=%d want 1031", rd)
	}
}

func TestBookReset(t *testing.T) {
	b := NewBook(1000)
	b.Append(Tick{Time: 1, Price: 1010})
	b.Reset(1081, Tick{Time: 131, Price: 1081})
	if b.Rs() != 1081 || b.Len() != 1 {
		t.Fatalf("reset 后 rs=%d len=%d want 1081,1", b.Rs(), b.Len())
	}
	if rd, _ := b.Rd(132, 10); rd != 1081 {
		t.Fatalf("reset 后记忆价应回落到 Rs=1081, got %d", rd)
	}
	if rd, _ := b.Rd(141, 10); rd != 1081 || b.Len() != 0 {
		t.Fatalf("老化 reset 记录后 Rd=%d len=%d want 1081,0", rd, b.Len())
	}
}
