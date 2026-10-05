package labrule

import "testing"

// 血钾规则：low=25, high=65, step=5。
func potassiumBook(t *testing.T) *Book {
	t.Helper()
	b := NewBook()
	if err := b.Add("K", 25, 65, 5); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return b
}

func TestSeverityTable(t *testing.T) {
	b := potassiumBook(t)
	cases := []struct {
		v        int64
		wantSev  int
		wantCrit bool
		why      string
	}{
		{25, 1, true, "取等 low 即危急，x=0"},
		{65, 1, true, "取等 high 即危急，x=0"},
		{26, 0, false, "low 内侧一格为正常"},
		{64, 0, false, "high 内侧一格为正常"},
		{50, 0, false, "区间中部为正常"},
		{66, 1, true, "x=1 < step"},
		{69, 1, true, "x=4 仍为一档"},
		{70, 2, true, "x=5 恰进二档"},
		{74, 2, true, "x=9 仍为二档"},
		{75, 3, true, "x=10 恰进三档"},
		{100, 3, true, "x=35 封顶三档"},
		{24, 1, true, "低侧 x=1"},
		{20, 2, true, "低侧 x=5 恰进二档"},
		{15, 3, true, "低侧 x=10 恰进三档"},
		{-1_000_000_000, 3, true, "极小值封顶三档"},
	}
	for _, c := range cases {
		sev, crit := b.Severity("K", c.v)
		if sev != c.wantSev || crit != c.wantCrit {
			t.Errorf("v=%d (%s): got (%d,%v), want (%d,%v)",
				c.v, c.why, sev, crit, c.wantSev, c.wantCrit)
		}
	}
}

func TestSeverityUnknownCode(t *testing.T) {
	b := potassiumBook(t)
	if sev, crit := b.Severity("NA", 10); crit || sev != 0 {
		t.Fatalf("unknown code: got (%d,%v), want (0,false)", sev, crit)
	}
}

func TestAddValidation(t *testing.T) {
	cases := []struct {
		name           string
		code           string
		low, high, stp int64
	}{
		{"空编码", "", 1, 2, 1},
		{"low 等于 high", "A", 5, 5, 1},
		{"low 大于 high", "B", 6, 5, 1},
		{"step 为 0", "C", 1, 2, 0},
		{"step 为负", "D", 1, 2, -3},
		{"step 超上界", "E", 1, 2, 1_000_001},
	}
	for _, c := range cases {
		b := NewBook()
		if err := b.Add(c.code, c.low, c.high, c.stp); err != ErrInvalid {
			t.Errorf("%s: got %v, want ErrInvalid", c.name, err)
		}
	}
	b := NewBook()
	if err := b.Add("K", 1, 2, 1_000_000); err != nil {
		t.Fatalf("step 取上界应合法: %v", err)
	}
	if err := b.Add("K", 1, 2, 1); err != ErrInvalid {
		t.Fatalf("重复注册: got %v, want ErrInvalid", err)
	}
}
