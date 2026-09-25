package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/poly"
)

// TestKnownVectors 钉住第三节八行表的结果。
func TestKnownVectors(t *testing.T) {
	muls := []struct{ a, b, want uint8 }{
		{0x57, 0x83, 0xC1}, {0x02, 0x80, 0x1B}, {0x53, 0x02, 0xA6},
		{0x0E, 0x0E, 0x54}, {0x9A, 0x9A, 0xC5},
	}
	for _, c := range muls {
		if got := api.Mul(c.a, c.b); got != c.want {
			t.Errorf("Mul(%02X,%02X)=%02X want %02X", c.a, c.b, got, c.want)
		}
	}
	invs := []struct{ a, want uint8 }{
		{0x53, 0xCA}, {0x02, 0x8D}, {0x03, 0xF6},
	}
	for _, c := range invs {
		if got, err := api.Inv(c.a); err != nil || got != c.want {
			t.Errorf("Inv(%02X)=%02X,%v want %02X", c.a, got, err, c.want)
		}
	}
}

// TestMulMatchesNaive 不变量 1：全部 65536 对与朴素「移位+归约」参照一致。
func TestMulMatchesNaive(t *testing.T) {
	ref, err := poly.NewReducer(0x1B)
	if err != nil {
		t.Fatal(err)
	}
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			if got, want := api.Mul(uint8(a), uint8(b)), ref.Mul(uint8(a), uint8(b)); got != want {
				t.Fatalf("Mul(%02X,%02X)=%02X want %02X", a, b, got, want)
			}
		}
	}
}

// TestInvRoundTrip 不变量 1：Mul(a, Inv(a)) == 0x01。
func TestInvRoundTrip(t *testing.T) {
	for a := 1; a < 256; a++ {
		iv, err := api.Inv(uint8(a))
		if err != nil || api.Mul(uint8(a), iv) != 0x01 {
			t.Errorf("Inv(%02X) round-trip failed: %02X %v", a, iv, err)
		}
	}
}

// TestFieldAxioms 不变量 2：交换律与分配律（确定性采样三元组）。
func TestFieldAxioms(t *testing.T) {
	for a := 0; a < 256; a += 13 {
		for b := 0; b < 256; b += 29 {
			if api.Mul(uint8(a), uint8(b)) != api.Mul(uint8(b), uint8(a)) {
				t.Fatalf("commutativity broken at %02X,%02X", a, b)
			}
			for c := 0; c < 256; c += 41 {
				l := api.Mul(uint8(a), api.Add(uint8(b), uint8(c)))
				r := api.Add(api.Mul(uint8(a), uint8(b)), api.Mul(uint8(a), uint8(c)))
				if l != r {
					t.Fatalf("distributivity broken at %02X,%02X,%02X", a, b, c)
				}
			}
		}
	}
}

// TestFermatLittle 不变量 3：群阶 255（a^255==1，故 a^256==a）且 Inv(a)==Pow(a,254)。
func TestFermatLittle(t *testing.T) {
	for a := 1; a < 256; a++ {
		p255, _ := api.Pow(uint8(a), 255)
		p256, _ := api.Pow(uint8(a), 256)
		iv, _ := api.Inv(uint8(a))
		p254, _ := api.Pow(uint8(a), 254)
		if p255 != 0x01 || p256 != uint8(a) || iv != p254 {
			t.Errorf("Fermat broken at %02X: ^255=%02X ^256=%02X Inv=%02X ^254=%02X",
				a, p255, p256, iv, p254)
		}
	}
}

// TestInvZeroError 不变量 4：Inv(0) 报哨兵错误，不 panic、不返回半成品、可重复。
func TestInvZeroError(t *testing.T) {
	for i := 0; i < 2; i++ {
		got, err := api.Inv(0x00)
		if !errors.Is(err, api.ErrZeroInverse) || got != 0x00 {
			t.Errorf("Inv(0)=%02X,%v want 0,ErrZeroInverse", got, err)
		}
	}
	if api.ErrZeroInverse == api.ErrExponentRange || api.ErrZeroInverse == api.ErrNotIrreducible ||
		api.ErrExponentRange == api.ErrNotIrreducible {
		t.Error("sentinel errors must be distinct")
	}
}

// TestConcurrentInvMul 不变量：并发与串行结果一致（无 sleep 人为时序）。
func TestConcurrentInvMul(t *testing.T) {
	type pair struct{ m, i uint8 }
	var want, got [256]pair
	for a := 1; a < 256; a++ {
		iv, _ := api.Inv(uint8(a))
		want[a] = pair{api.Mul(uint8(a), 0x53), iv}
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for a := g + 1; a < 256; a += 16 {
				iv, _ := api.Inv(uint8(a))
				got[a] = pair{api.Mul(uint8(a), 0x53), iv}
			}
		}(g)
	}
	wg.Wait()
	if got != want {
		t.Error("concurrent results differ from serial")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
