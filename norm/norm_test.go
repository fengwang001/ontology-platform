package norm

import "testing"

func i64(v int64) *int64 { return &v }

func TestNewRejectsBadParams(t *testing.T) {
	for _, sd := range []int{-1, 7} {
		if _, err := New(sd, RMHalfUp, false); err == nil {
			t.Fatalf("sd=%d must be rejected", sd)
		}
	}
	for _, rm := range []int{-1, 3} {
		if _, err := New(2, rm, false); err == nil {
			t.Fatalf("rm=%d must be rejected", rm)
		}
	}
}

func TestCvtD(t *testing.T) {
	// 规格例三与例一：sd=2（D=10000）
	cases := []struct {
		rm int
		m  int64
		q  int64
	}{
		{RMHalfUp, 125000, 13},
		{RMHalfUp, -125000, -13},
		{RMHalfUp, 135000, 14},
		{RMHalfEven, 125000, 12},
		{RMHalfEven, -125000, -12},
		{RMHalfEven, 135000, 14},
		{RMHalfEven, -135000, -14},
		{RMTruncate, 125000, 12},
		{RMTruncate, 135000, 13},
		{RMTruncate, -135000, -13},
		{RMHalfUp, 5000, 1}, // 例：id 4 的 5000 -> 1
		{RMHalfUp, 4999, 0},
		{RMHalfUp, -4999, 0},
		{RMHalfUp, -5000, -1},
		{RMHalfEven, 5000, 0},  // q=0 偶，不舍
		{RMHalfEven, -5000, 0}, // q=0 偶
		{RMHalfUp, 0, 0},
	}
	for _, tc := range cases {
		n, _ := New(2, tc.rm, false)
		got := n.CvtD(i64(tc.m))
		if got == nil || *got != tc.q {
			t.Fatalf("rm=%d m=%d: got %v want %d", tc.rm, tc.m, got, tc.q)
		}
	}
}

func TestCvtDNullAndIdentity(t *testing.T) {
	n, _ := New(6, RMHalfUp, false)
	if got := n.CvtD(nil); got != nil {
		t.Fatalf("NULL must stay NULL")
	}
	v := n.CvtD(i64(-123456789))
	if v == nil || *v != -123456789 {
		t.Fatalf("sd=6 must be identity, got %v", v)
	}
	// sd=0（D=10^6）也覆盖一档
	n0, _ := New(0, RMHalfUp, false)
	if *n0.CvtD(i64(1_500_000)) != 2 || *n0.CvtD(i64(-1_500_000)) != -2 {
		t.Fatalf("sd=0 half-up at exact half wrong")
	}
}

func TestNormC(t *testing.T) {
	n, _ := New(2, RMHalfUp, false)
	if got := n.NormC(nil); got != nil {
		t.Fatalf("NULL stays NULL")
	}
	// 只去右侧 0x20，左侧空格与制表符保留
	if got := string(n.NormC([]byte(" ab "))); got != " ab" {
		t.Fatalf("trim right only, got %q", got)
	}
	if got := string(n.NormC([]byte("\tab  "))); got != "\tab" {
		t.Fatalf("tab must be kept, got %q", got)
	}
	if got := n.NormC([]byte("   ")); got == nil || len(got) != 0 {
		t.Fatalf("all spaces -> empty string (ne=false), got %v", got)
	}
	if got := n.NormC([]byte{}); got == nil || len(got) != 0 {
		t.Fatalf("empty string stays empty (ne=false)")
	}

	nne, _ := New(2, RMHalfUp, true)
	if got := nne.NormC([]byte("   ")); got != nil {
		t.Fatalf("ne=true: blank string -> NULL, got %v", got)
	}
	if got := nne.NormC([]byte{}); got != nil {
		t.Fatalf("ne=true: empty string -> NULL")
	}
	if got := string(nne.NormC([]byte(" x "))); got != " x" {
		t.Fatalf("ne=true still trims right, got %q", got)
	}
}

func TestCheckRow(t *testing.T) {
	if err := CheckRow(1, i64(MaxM), make([]byte, 64)); err != nil {
		t.Fatalf("boundary values must pass: %v", err)
	}
	if err := CheckRow(0, nil, nil); err == nil {
		t.Fatalf("id=0 rejected")
	}
	if err := CheckRow(1_000_000_001, nil, nil); err == nil {
		t.Fatalf("id>1e9 rejected")
	}
	if err := CheckRow(1, i64(MaxM+1), nil); err == nil {
		t.Fatalf("|m|>1e15 rejected")
	}
	if err := CheckRow(1, i64(-MaxM-1), nil); err == nil {
		t.Fatalf("|m|>1e15 rejected (neg)")
	}
	if err := CheckRow(1, nil, make([]byte, 65)); err == nil {
		t.Fatalf("c longer than 64 rejected")
	}
}
