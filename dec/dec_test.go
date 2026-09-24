package dec

import (
	"math"
	"math/big"
	"testing"

	"ontology/bits"
)

func decOf(t *testing.T, x float64) Decimal {
	t.Helper()
	d, err := Shortest(x)
	if err != nil {
		t.Fatalf("Shortest(%v): %v", x, err)
	}
	return d
}

func TestShortestTable(t *testing.T) {
	cases := []struct {
		name   string
		x      float64
		digits string
		exp    int
		ndig   int
	}{
		{"0.1", 0.1, "1", -1, 1},
		{"-0.1", -0.1, "1", -1, 1},
		{"1/3", 1.0 / 3.0, "3333333333333333", -16, 16},
		{"1", 1, "1", 0, 1},
		{"1.5", 1.5, "15", -1, 2},
		{"100", 100, "1", 2, 1},
		{"max", math.MaxFloat64, "17976931348623157", 292, 17},
		{"min sub", math.SmallestNonzeroFloat64, "5", -324, 1},
		{"2^53", 1 << 53, "9007199254740992", 0, 16},
		{"2^53+2", 1<<53 + 2, "9007199254740994", 0, 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decOf(t, c.x)
			if d.DigitsText() != c.digits || d.Exp != c.exp || len(d.DigitsText()) != c.ndig {
				t.Fatalf("Shortest(%v) = %s e%d (%d 位), want %s e%d (%d 位)",
					c.x, d.DigitsText(), d.Exp, len(d.DigitsText()), c.digits, c.exp, c.ndig)
			}
			if RoundBits(d.Neg, d.Digits, d.Exp) != math.Float64bits(c.x) {
				t.Fatalf("%v: 精确解回位模式不一致", c.x)
			}
		})
	}
}

func TestPointOneIsOneDigit(t *testing.T) {
	d := decOf(t, 0.1)
	if d.DigitsText() != "1" || d.Exp != -1 {
		t.Fatalf("0.1 编成了 %se%d，必须是 1e-1", d.DigitsText(), d.Exp)
	}
}

func TestProbeCountPointOne(t *testing.T) {
	before := Count()
	decOf(t, 0.1)
	if got := Count() - before; got > 3 {
		t.Fatalf("0.1 用了 %d 次精确往返检查，必须不超过 3", got)
	}
}

func TestSpecialErrors(t *testing.T) {
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Shortest(x); err != ErrSpecial {
			t.Fatalf("Shortest(%v) err = %v, want ErrSpecial", x, err)
		}
	}
}

func TestZeroSign(t *testing.T) {
	pz := decOf(t, 0)
	nz := decOf(t, math.Copysign(0, -1))
	if pz.Neg || !nz.Neg || pz.Digits.Sign() != 0 || nz.Digits.Sign() != 0 {
		t.Fatalf("正负零未区分: %+v %+v", pz, nz)
	}
}

// TestFloatBackSubTrap 固定一个浮点回代误判值：
// float 算术认为 x*1e3 恰为整数 ...773；精确算术 floor 为 ...772 且有余数。
func TestFloatBackSubTrap(t *testing.T) {
	x := math.Float64frombits(0x428ccf91036fd62f) // 3.959727091194773e+12
	fp := x * 1e3
	K := int64(fp)
	if fp != float64(K) {
		t.Fatal("前置假设失效：浮点乘积不是整数")
	}
	p := bits.Split(x)
	num, den := new(big.Int).SetUint64(p.Mant), big.NewInt(1)
	den.Lsh(den, uint(-p.Exp))
	num.Mul(num, new(big.Int).Exp(big.NewInt(10), big.NewInt(3), nil))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(num, den, rem)
	if q.Int64() == K {
		t.Fatalf("该值不再构成误判：精确 floor 也是 %d", K)
	}
	if rem.Sign() == 0 {
		t.Fatal("精确结果意外为整数")
	}
	d := decOf(t, x)
	if RoundBits(d.Neg, d.Digits, d.Exp) != math.Float64bits(x) || len(d.DigitsText()) > 17 {
		t.Fatalf("误判值的最短表示错误：%s e%d", d.DigitsText(), d.Exp)
	}
	if d.DigitsText() != "3959727091194773" {
		t.Fatalf("误判值给出系数 %s，want 3959727091194773", d.DigitsText())
	}
}
