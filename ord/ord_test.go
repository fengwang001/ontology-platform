package ord

import "testing"

// 朴素教科书参照：逐字节移位写、逐字节拼装读、逐字节反转。
func naivePut(o ByteOrder, b []byte, v uint64, w int) {
	for i := 0; i < w; i++ {
		if o == BigEndian {
			b[i] = byte(v >> (8 * (w - 1 - i)))
		} else {
			b[i] = byte(v >> (8 * i))
		}
	}
}

func naiveGet(o ByteOrder, b []byte, w int) uint64 {
	var v uint64
	for i := 0; i < w; i++ {
		if o == BigEndian {
			v |= uint64(b[i]) << (8 * (w - 1 - i))
		} else {
			v |= uint64(b[i]) << (8 * i)
		}
	}
	return v
}

func naiveSwap(v uint64, w int) uint64 {
	var out []byte
	in := make([]byte, w)
	naivePut(LittleEndian, in, v, w)
	for i := w - 1; i >= 0; i-- {
		out = append(out, in[i])
	}
	return naiveGet(LittleEndian, out, w)
}

var vectors = []uint64{0, 1, 0x7F, 0x80, 0xFF, 0x0102, 0x8001, 0x01020304,
	0x80000001, 0x0102030405060708, 0x8000000000000001, 0xFFFFFFFFFFFFFFFF}

// 不变量 3：Put/Uint 与朴素参照字节级一致（含符号扩展读取）。
func TestNaiveReference(t *testing.T) {
	for _, o := range []ByteOrder{BigEndian, LittleEndian} {
		for _, v := range vectors {
			for w := 2; w <= 8; w *= 2 {
				got, want := make([]byte, w), make([]byte, w)
				mask := uint64(0xFFFFFFFFFFFFFFFF) >> (64 - 8*w)
				u := v & mask
				naivePut(o, want, u, w)
				switch w {
				case 2:
					PutUint16(o, got, uint16(u))
				case 4:
					PutUint32(o, got, uint32(u))
				case 8:
					PutUint64(o, got, u)
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("order=%d w=%d v=%#x: byte %d got %#x want %#x", o, w, v, i, got[i], want[i])
					}
				}
				var r uint64
				switch w {
				case 2:
					r = uint64(Uint16(o, got))
				case 4:
					r = uint64(Uint32(o, got))
				case 8:
					r = Uint64(o, got)
				}
				if r != naiveGet(o, want, w) {
					t.Fatalf("order=%d w=%d v=%#x: read %#x want %#x", o, w, v, r, naiveGet(o, want, w))
				}
			}
		}
	}
	// 符号扩展：最高字节 bit7=1 读回为负
	if got := Int16(BigEndian, []byte{0xFF, 0xFF}); got != -1 {
		t.Fatalf("Int16 sign extension: got %d want -1", got)
	}
	if got := Int32(LittleEndian, []byte{0xFF, 0xFF, 0xFF, 0xFF}); got != -1 {
		t.Fatalf("Int32 sign extension: got %d want -1", got)
	}
	if got := Int64(BigEndian, []byte{0x80, 0, 0, 0, 0, 0, 0, 0}); got != -9223372036854775808 {
		t.Fatalf("Int64 sign extension: got %d want MinInt64", got)
	}
}

// 不变量 2：Swap 互逆；BE 写 LE 读 = 字节反转。
func TestSwapAndCrossOrder(t *testing.T) {
	for _, v := range vectors {
		if Swap16(Swap16(uint16(v))) != uint16(v) ||
			Swap32(Swap32(uint32(v))) != uint32(v) ||
			Swap64(Swap64(v)) != v {
			t.Fatalf("Swap(Swap(%#x)) != v", v)
		}
		if uint64(Swap32(uint32(v))) != naiveSwap(v&0xFFFFFFFF, 4) ||
			Swap64(v) != naiveSwap(v, 8) || uint64(Swap16(uint16(v))) != naiveSwap(v&0xFFFF, 2) {
			t.Fatalf("Swap(%#x) disagrees with naive byte reversal", v)
		}
		var b [8]byte
		PutUint64(BigEndian, b[:], v)
		if Uint64(LittleEndian, b[:]) != Swap64(v) {
			t.Fatalf("BE-write LE-read of %#x != Swap64", v)
		}
		PutUint32(LittleEndian, b[:4], uint32(v))
		if Uint32(BigEndian, b[:4]) != Swap32(uint32(v)) {
			t.Fatalf("LE-write BE-read of %#x != Swap32", v)
		}
	}
}
