package api_test

import (
	"errors"
	"testing"

	"ontology/api"
	"ontology/field"
	"ontology/poly"
)

func TestGoldenOps(t *testing.T) {
	muls := []struct{ a, b, want uint8 }{
		{0x57, 0x83, 0xC1}, {0x02, 0x80, 0x1B}, {0x53, 0x02, 0xA6},
		{0x0E, 0x0E, 0x54}, {0x9A, 0x9A, 0xC5},
	}
	for _, c := range muls {
		if got := api.Mul(c.a, c.b); got != c.want {
			t.Errorf("Mul(%02x,%02x)=%02x want %02x", c.a, c.b, got, c.want)
		}
	}
	invs := []struct{ a, want uint8 }{
		{0x53, 0xCA}, {0x02, 0x8D}, {0x03, 0xF6},
	}
	for _, c := range invs {
		if got, err := api.Inv(c.a); err != nil || got != c.want {
			t.Errorf("Inv(%02x)=%02x,%v want %02x", c.a, got, err, c.want)
		}
	}
	if api.Add(0x53, 0xCA) != 0x53^0xCA {
		t.Error("Add 应为异或")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestPolyInvalidParam(t *testing.T) {
	bad := []uint8{0x00, 0x02, 0x1C, 0xFF} // 偶数 popcount 或含低次因子的可约多项式
	for _, b := range bad {
		if _, err := poly.New(b); !errors.Is(err, poly.ErrInvalidPolynomial) {
			t.Errorf("poly.New(%02x) 应报 ErrInvalidPolynomial, got %v", b, err)
		}
	}
	good := []uint8{0x1B, 0x1D}
	for _, g := range good {
		if _, err := poly.New(g); err != nil {
			t.Errorf("poly.New(%02x) 应成功, got %v", g, err)
		}
	}
}

func TestErrorsDistinct(t *testing.T) {
	errs := []error{api.ErrZeroInverse, api.ErrExponentTooLarge, poly.ErrInvalidPolynomial}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("哨兵错误 %v 与 %v 必须互不相同", errs[i], errs[j])
			}
		}
	}
	if _, err := api.Inv(0x00); !errors.Is(err, api.ErrZeroInverse) {
		t.Fatalf("api.Inv(0) 应报 ErrZeroInverse, got %v", err)
	}
	if _, err := api.Pow(0x02, 1<<63+1); !errors.Is(err, api.ErrExponentTooLarge) {
		t.Fatalf("api.Pow 超界应报 ErrExponentTooLarge, got %v", err)
	}
	if !errors.Is(api.ErrZeroInverse, field.ErrZeroInverse) {
		t.Fatal("api 与 field 的哨兵错误应同源")
	}
}
