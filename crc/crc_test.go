package crc

import (
	"math/rand"
	"testing"

	"ontology/poly"
)

// 不变量 1：任意字节序列，表驱动与逐位参照逐字节相同。
func TestTableMatchesBitwise(t *testing.T) {
	tab := NewTable(poly.IEEE)
	r := rand.New(rand.NewSource(1))
	cases := [][]byte{{}, {0x00}, {0xFF}, []byte("123456789")}
	for i := 0; i < 8; i++ { // 随机字节序列用循环生成
		buf := make([]byte, 1+r.Intn(1024))
		r.Read(buf)
		cases = append(cases, buf)
	}
	for _, in := range cases {
		a, b := NewDigest(tab, 0xFFFFFFFF), NewDigest(tab, 0xFFFFFFFF)
		a.UpdateTable(in)
		b.UpdateBitwise(in)
		if a.Raw() != b.Raw() {
			t.Fatalf("len=%d: 表驱动 %08X != 逐位 %08X", len(in), a.Raw(), b.Raw())
		}
	}
}

// 表的第 1 项是公开文献值。
func TestTableEntry(t *testing.T) {
	tab := NewTable(poly.IEEE)
	if tab.t[1] != 0x77073096 {
		t.Fatalf("table[1]=%08X want 77073096", tab.t[1])
	}
}

// 复杂度约束：表驱动更新不论 m 多大，逐位循环次数恒为 0；
// 逐位参照则恰为 8*m（对照组，证明计数器本身工作）。
func TestBitwiseLoopsZero(t *testing.T) {
	tab := NewTable(poly.IEEE)
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		d := NewDigest(tab, 0xFFFFFFFF)
		d.UpdateTable(make([]byte, m))
		if d.lastBitwise != 0 {
			t.Fatalf("m=%d: 表驱动后 lastBitwise=%d want 0", m, d.lastBitwise)
		}
		d.UpdateBitwise(make([]byte, m))
		if d.lastBitwise != 8*m {
			t.Fatalf("m=%d: 逐位后 lastBitwise=%d want %d", m, d.lastBitwise, 8*m)
		}
	}
}

// 换用其他合法反射多项式（如 CRC-32C 的反射形式）表驱动仍等于逐位。
func TestCustomPoly(t *testing.T) {
	const castagnoli = 0x82F63B78
	if !poly.Valid(castagnoli) {
		t.Fatal("castagnoli 应合法")
	}
	tab := NewTable(castagnoli)
	in := []byte("custom-poly")
	a, b := NewDigest(tab, 0xFFFFFFFF), NewDigest(tab, 0xFFFFFFFF)
	a.UpdateTable(in)
	b.UpdateBitwise(in)
	if a.Raw() != b.Raw() {
		t.Fatalf("自定义 poly: %08X != %08X", a.Raw(), b.Raw())
	}
}
