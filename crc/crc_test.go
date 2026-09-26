package crc

import (
	"math/rand"
	"testing"
)

func TestKnownVector(t *testing.T) {
	c := New(0xEDB88320)
	c.UpdateTable([]byte("123456789"))
	if got := c.Value(); got != 0xCBF43926 {
		t.Fatalf("Value() = %#08x, want 0xcbf43926", got)
	}
}

// 表驱动必须与逐位参照逐字节一致（不变量 1）。
func TestTableMatchesBitwise(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for _, n := range []int{0, 1, 2, 7, 8, 9, 255, 256, 1000, 4096} {
		buf := make([]byte, n)
		r.Read(buf)
		tab, bit := New(0xEDB88320), New(0xEDB88320)
		tab.UpdateTable(buf)
		bit.UpdateBitwise(buf)
		if tab.Value() != bit.Value() {
			t.Fatalf("n=%d: table %#08x != bitwise %#08x", n, tab.Value(), bit.Value())
		}
	}
}

// 表驱动更新走过 0 次逐位循环，与 m 无关（复杂度证明）。
func TestBitLoopCounter(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		buf := make([]byte, m)
		r.Read(buf)
		c := New(0xEDB88320)
		c.UpdateTable(buf)
		if c.bitLoops != 0 {
			t.Fatalf("m=%d: UpdateTable 后 bitLoops = %d, want 0", m, c.bitLoops)
		}
		c.UpdateBitwise(buf)
		if c.bitLoops != 8*m {
			t.Fatalf("m=%d: UpdateBitwise 后 bitLoops = %d, want %d", m, c.bitLoops, 8*m)
		}
	}
}
