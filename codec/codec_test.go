package codec

import (
	"bytes"
	"slices"
	"testing"

	"ontology/ord"
)

var orders = []ord.ByteOrder{ord.BigEndian, ord.LittleEndian}

// shift 是教科书参照实现里第 i 个字节的移位量。
func shift(o ord.ByteOrder, w, i int) uint {
	if o == ord.BigEndian {
		return uint(w-1-i) * 8
	}
	return uint(i) * 8
}

func naivePut(o ord.ByteOrder, w int, b []byte, v uint64) {
	for i := 0; i < w; i++ {
		b[i] = byte(v >> shift(o, w, i))
	}
}

func naiveGet(o ord.ByteOrder, w int, b []byte) uint64 {
	var v uint64
	for i := 0; i < w; i++ {
		v |= uint64(b[i]) << shift(o, w, i)
	}
	return v
}

// gen 用 LCG 生成 n 个落在 w 字节有符号范围内的值。
func gen(w, n int) []int64 {
	out := make([]int64, n)
	x := uint64(0x9E3779B97F4A7C15)
	for i := range out {
		x = x*6364136223846793005 + 1442695040888963407
		out[i] = int64(x) << uint(64-8*w) >> uint(64-8*w)
	}
	return out
}

// lowMask 屏蔽 get 的符号扩展位，便于与零扩展参照比较。
func lowMask(w int) uint64 { return 1<<(uint(w)*8) - 1 }

func TestNaiveReference(t *testing.T) {
	for _, w := range []int{2, 4, 8} {
		for _, o := range orders {
			for _, v := range gen(w, 50) {
				got, ref := make([]byte, w), make([]byte, w)
				put(o, w, got, v)
				naivePut(o, w, ref, uint64(v))
				if !bytes.Equal(got, ref) || uint64(get(o, w, got))&lowMask(w) != naiveGet(o, w, got) {
					t.Fatalf("w=%d o=%v v=%d: 与朴素参照不一致", w, o, v)
				}
			}
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, w := range []int{2, 4, 8} {
		for _, o := range orders {
			vals := gen(w, 200)
			back, err := Decode(o, w, Encode(o, w, vals))
			if err != nil || !slices.Equal(back, vals) {
				t.Fatalf("w=%d o=%v: err=%v", w, o, err)
			}
		}
	}
}

func TestSwapTwice(t *testing.T) {
	for _, v := range gen(8, 100) {
		u := uint64(v)
		if ord.Swap64(ord.Swap64(u)) != u ||
			ord.Swap32(ord.Swap32(uint32(u))) != uint32(u) ||
			ord.Swap16(ord.Swap16(uint16(u))) != uint16(u) {
			t.Fatalf("Swap 两次未还原: %d", v)
		}
	}
}

func TestCrossOrderIsSwap(t *testing.T) {
	for _, w := range []int{2, 4, 8} {
		for _, v := range gen(w, 50) {
			be := Encode(ord.BigEndian, w, []int64{v})
			got := uint64(get(ord.LittleEndian, w, be)) & lowMask(w)
			tmp := make([]byte, w)
			naivePut(ord.LittleEndian, w, tmp, uint64(v))
			if want := naiveGet(ord.BigEndian, w, tmp); got != want {
				t.Fatalf("w=%d v=%d: %#x != 字节反转 %#x", w, v, got, want)
			}
		}
	}
}

func TestRejectNoPartial(t *testing.T) {
	for _, w := range []int{-2, 0, 1, 3, 5, 16} {
		if got, err := Decode(ord.BigEndian, w, make([]byte, 24)); got != nil || err != ErrInvalidWidth {
			t.Fatalf("width=%d: got=%v err=%v", w, got, err)
		}
	}
	for _, w := range []int{2, 4, 8} {
		if got, err := Decode(ord.LittleEndian, w, make([]byte, w+1)); got != nil || err != ErrMisaligned {
			t.Fatalf("w=%d 不对齐: got=%v err=%v", w, got, err)
		}
	}
	if ErrInvalidWidth == ErrMisaligned {
		t.Fatal("两类错误必须互不相同")
	}
	if _, err := Decode(ord.BigEndian, 4, make([]byte, 8)); err != nil { // 拒绝后仍可用
		t.Fatalf("拒绝后无法正常使用: %v", err)
	}
}

func TestCursorNoLookback(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		vals := gen(8, m)
		buf := Encode(ord.LittleEndian, 8, vals)
		d, _ := NewDecoder(ord.LittleEndian, 8, buf)
		for i := 0; i < m; i++ {
			if v, ok := d.Next(); !ok || v != vals[i] {
				t.Fatalf("m=%d i=%d: got %d,%v", m, i, v, ok)
			}
		}
		if d.examined != len(buf) || d.lookback != 0 {
			t.Fatalf("m=%d: 检查数 %d 长度 %d 回看 %d", m, d.examined, len(buf), d.lookback)
		}
	}
}
