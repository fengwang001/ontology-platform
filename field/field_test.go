package field

import (
	"errors"
	"math/bits"
	"sync"
	"testing"
)

// naive 是测试内的朴素参照：逐位多项式乘再对 0x11B 手工归约。
func naive(a, b uint8) uint8 {
	r, aa := 0, int(a)
	for b != 0 {
		if b&1 != 0 {
			r ^= aa
		}
		b >>= 1
		aa <<= 1
		if aa&0x100 != 0 {
			aa ^= 0x11B
		}
	}
	return uint8(r)
}

func TestMulMatchesNaive(t *testing.T) {
	golden := []struct{ a, b, want uint8 }{
		{0x57, 0x83, 0xC1}, {0x02, 0x80, 0x1B}, {0x53, 0x02, 0xA6},
		{0x0E, 0x0E, 0x54}, {0x9A, 0x9A, 0xC5}, {0x00, 0x53, 0x00},
	}
	for _, g := range golden {
		if got := Mul(g.a, g.b); got != g.want {
			t.Fatalf("Mul(%02x,%02x)=%02x want %02x", g.a, g.b, got, g.want)
		}
	}
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			if got, want := Mul(uint8(a), uint8(b)), naive(uint8(a), uint8(b)); got != want {
				t.Fatalf("Mul(%02x,%02x)=%02x want naive %02x", a, b, got, want)
			}
		}
	}
}

func TestInvMultiplyIdentity(t *testing.T) {
	for a := 1; a < 256; a++ {
		inv, err := Inv(uint8(a))
		if err != nil || Mul(uint8(a), inv) != 0x01 {
			t.Fatalf("Inv(%02x)=%02x,%v 不满足 a*Inv(a)==1", a, inv, err)
		}
	}
}

func TestFieldAxioms(t *testing.T) {
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			if Mul(uint8(a), uint8(b)) != Mul(uint8(b), uint8(a)) {
				t.Fatalf("交换律失败 a=%02x b=%02x", a, b)
			}
		}
	}
	samples := []uint8{0x00, 0x01, 0x02, 0x03, 0x53, 0x57, 0x83, 0xCA, 0xFF}
	for _, a := range samples {
		for _, b := range samples {
			for _, c := range samples {
				if Mul(a, Add(b, c)) != Add(Mul(a, b), Mul(a, c)) {
					t.Fatalf("分配律失败 a=%02x b=%02x c=%02x", a, b, c)
				}
			}
		}
	}
}

func TestFermatAndInvPow(t *testing.T) {
	for a := 1; a < 256; a++ {
		inv, _ := Inv(uint8(a))
		p254, _ := Pow(uint8(a), 254)
		p255, _ := Pow(uint8(a), 255)
		p256, _ := Pow(uint8(a), 256)
		if p255 != 0x01 || p256 != uint8(a) || inv != p254 {
			t.Fatalf("a=%02x: a^255=%02x a^256=%02x Inv=%02x a^254=%02x", a, p255, p256, inv, p254)
		}
	}
}

func TestInvZeroError(t *testing.T) {
	v, err := Inv(0x00)
	if !errors.Is(err, ErrZeroInverse) || v != 0 {
		t.Fatalf("Inv(0)=%02x,%v 应为 ErrZeroInverse 且无半成品", v, err)
	}
	if errors.Is(ErrZeroInverse, ErrExponentTooLarge) {
		t.Fatal("哨兵错误必须互不相同")
	}
}

func TestPowEdgeAndError(t *testing.T) {
	cases := [][3]uint64{{0x00, 0, 0x01}, {0x53, 0, 0x01}, {0x00, 5, 0x00}, {0x02, 1, 0x02}}
	for _, c := range cases {
		if got, err := Pow(uint8(c[0]), c[1]); err != nil || got != uint8(c[2]) {
			t.Fatalf("Pow(%02x,%d)=%02x,%v want %02x", c[0], c[1], got, err, c[2])
		}
	}
	if _, err := Pow(0x02, 1<<63+1); !errors.Is(err, ErrExponentTooLarge) {
		t.Fatalf("e>1<<63 应报 ErrExponentTooLarge, got %v", err)
	}
}

func TestPowMulCount(t *testing.T) {
	for e := uint64(100); e <= 10000; e = e*10 + e/10 { // 100,1100,10000 档
		if _, err := Pow(0x53, e); err != nil {
			t.Fatal(err)
		}
		if got, limit := powMuls.Load(), int64(2*bits.Len64(e-1)+1); got > limit {
			t.Fatalf("Pow(.,%d) 乘法次数 %d 超过上界 %d", e, got, limit)
		}
	}
}

func TestConcurrentInvMul(t *testing.T) {
	var serial [256]uint8
	for a := 1; a < 256; a++ {
		serial[a], _ = Inv(uint8(a))
	}
	var con [32][256]uint8
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for a := 1; a < 256; a++ {
				v, _ := Inv(uint8(a))
				if Mul(uint8(a), v) != 1 {
					t.Error("并发下 a*Inv(a)!=1")
					return
				}
				con[g][a] = v
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < 32; g++ {
		if con[g] != serial {
			t.Fatal("并发结果与串行不一致")
		}
	}
}
