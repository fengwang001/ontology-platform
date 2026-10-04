package keyenc

import (
	"bytes"
	"testing"
)

func TestE2OrderMatchesLogical(t *testing.T) {
	keys := []int64{-1_000_000_000_000, -5, -1, 0, 1, 7, 1_000_000_000_000}
	for i := 0; i+1 < len(keys); i++ {
		a, b := E2(keys[i]), E2(keys[i+1])
		if bytes.Compare(a[:], b[:]) >= 0 {
			t.Fatalf("E2 不保序：%d >= %d", keys[i], keys[i+1])
		}
	}
}

func TestE1NegativesSortAfterNonNegatives(t *testing.T) {
	neg, zero := E1(-1), E1(0)
	if bytes.Compare(neg[:], zero[:]) <= 0 {
		t.Fatal("E1 下负数应排在非负数之后")
	}
	a, b := E1(-5), E1(-1)
	if bytes.Compare(a[:], b[:]) >= 0 {
		t.Fatal("E1 下同符号内应保序")
	}
}

func TestRoundTrip(t *testing.T) {
	for _, k := range []int64{-1_000_000_000_000, -1, 0, 1, 1_000_000_000_000} {
		if e := E1(k); Decode1(e[:]) != k {
			t.Fatalf("Decode1(E1(%d)) 不符", k)
		}
		if e := E2(k); Decode2(e[:]) != k {
			t.Fatalf("Decode2(E2(%d)) 不符", k)
		}
	}
}

func TestRange1Split(t *testing.T) {
	// 纯负区间且上界为 0：物理上界是无穷。
	ivs := Range1(-3, 0)
	if len(ivs) != 1 || ivs[0].Hi != nil {
		t.Fatalf("Range1(-3,0)=%+v, 想要单区间且 Hi=nil", ivs)
	}
	if lo := E1(-3); !bytes.Equal(ivs[0].Lo, lo[:]) {
		t.Fatal("Range1(-3,0) 下界应为 E1(-3)")
	}
	// 跨 0 区间：拆成负数段（上界无穷）与非负段。
	ivs = Range1(-3, 5)
	if len(ivs) != 2 || ivs[0].Hi != nil {
		t.Fatalf("Range1(-3,5)=%+v, 想要两段且负段 Hi=nil", ivs)
	}
	lo1, hi1 := E1(0), E1(5)
	if !bytes.Equal(ivs[1].Lo, lo1[:]) || !bytes.Equal(ivs[1].Hi, hi1[:]) {
		t.Fatal("Range1(-3,5) 非负段应为 [E1(0), E1(5))")
	}
	// 纯负区间上界非 0：物理上界为 E1(hi)。
	ivs = Range1(-5, -2)
	hi := E1(-2)
	if len(ivs) != 1 || !bytes.Equal(ivs[0].Hi, hi[:]) {
		t.Fatalf("Range1(-5,-2)=%+v, 想要单区间 Hi=E1(-2)", ivs)
	}
	// 空区间。
	if Range1(3, 3) != nil || Range1(5, 2) != nil {
		t.Fatal("空逻辑区间应映射为空")
	}
	// 纯非负区间。
	ivs = Range1(2, 9)
	if len(ivs) != 1 {
		t.Fatalf("Range1(2,9)=%+v, 想要单区间", ivs)
	}
}

func TestRange2SingleInterval(t *testing.T) {
	ivs := Range2(-3, 5)
	lo, hi := E2(-3), E2(5)
	if len(ivs) != 1 || !bytes.Equal(ivs[0].Lo, lo[:]) || !bytes.Equal(ivs[0].Hi, hi[:]) {
		t.Fatalf("Range2(-3,5)=%+v", ivs)
	}
	if Range2(4, 4) != nil {
		t.Fatal("空逻辑区间应映射为空")
	}
}
