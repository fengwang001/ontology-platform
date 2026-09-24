package dec

import (
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"testing"
)

func TestShortest(t *testing.T) {
	cases := []struct {
		x      float64
		digits string
		exp    int
		neg    bool
	}{
		{0.1, "1", -1, false},
		{1.0 / 3.0, "3333333333333333", -1, false},
		{1.0, "1", 0, false},
		{-2.5, "25", 0, true},
		{100, "1", 2, false},
		{1e16, "1", 16, false},
		{1e17, "1", 17, false},
		{math.MaxFloat64, "17976931348623157", 308, false},
		{math.SmallestNonzeroFloat64, "5", -324, false},
		{1 << 53, "9007199254740992", 15, false},
		{1<<53 - 1, "9007199254740991", 15, false},
		{1<<53 + 2, "9007199254740994", 15, false},
	}
	for _, c := range cases {
		d, err := Shortest(c.x)
		if err != nil {
			t.Fatalf("Shortest(%v): %v", c.x, err)
		}
		if d.Digits != c.digits || d.Exp != c.exp || d.Neg != c.neg {
			t.Errorf("Shortest(%v) = %+v, want digits=%s exp=%d neg=%v",
				c.x, d, c.digits, c.exp, c.neg)
		}
	}
}

func TestChecksCounter(t *testing.T) {
	ResetChecks()
	if _, err := Shortest(0.1); err != nil {
		t.Fatal(err)
	}
	if got := Checks(); got > 3 {
		t.Errorf("0.1 的往返检查次数 = %d，应不超过 3", got)
	}
}

// floatEval 用浮点乘除回代文本，模拟会误判的朴素验证。
func floatEval(m int64, exp int) float64 {
	f := float64(m)
	for ; exp > 0; exp-- {
		f *= 10
	}
	for ; exp < 0; exp++ {
		f /= 10
	}
	return f
}

func TestFloatBacksubMisjudges(t *testing.T) {
	x := math.Float64frombits(0x5d78399cbed80a3a) // 1.8463014761648678e+142
	// 十六位表示 1.846301476164868e+142：浮点回代看似相等。
	if got := floatEval(1846301476164868, 142-15); got != x {
		t.Fatal("前提失效：浮点回代未误判")
	}
	// 精确比较（标准库参考）不等，正确答案必须是十七位。
	y, _ := strconv.ParseFloat("1.846301476164868e+142", 64)
	if y == x {
		t.Fatal("前提失效：十六位精确可往返")
	}
	d, err := Shortest(x)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Digits) != 17 || d.Digits != "18463014761648678" {
		t.Errorf("Shortest(x) = %s（%d 位），want 18463014761648678（17 位）",
			d.Digits, len(d.Digits))
	}
}

// TestInInterval 随机抽样，断言生成的十进制值精确落在舍入区间内。
func TestInInterval(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 20000; i++ {
		x := math.Float64frombits(r.Uint64())
		if bits0 := x; math.IsNaN(bits0) || math.IsInf(bits0, 0) || x == 0 {
			continue
		}
		d, err := Shortest(x)
		if err != nil {
			t.Fatalf("Shortest(%v): %v", x, err)
		}
		if len(d.Digits) > 17 {
			t.Fatalf("%v: 有效数字 %d 位超过 17", x, len(d.Digits))
		}
		m, ok := new(big.Int).SetString(d.Digits, 10)
		if !ok {
			t.Fatal("Digits 非法")
		}
		k := d.Exp - (len(d.Digits) - 1)
		val := new(big.Rat).SetInt(m)
		val.Mul(val, ratPow10(k))
		if d.Neg != math.Signbit(x) {
			t.Fatalf("%v: 符号位错误", x)
		}
		lo, hi, loC, hiC := interval(x)
		if !inInterval(val, lo, hi, loC, hiC) {
			t.Fatalf("%v: 生成的值 %s*10^%d 不在舍入区间内", x, d.Digits, k)
		}
	}
}

func inInterval(v, lo, hi *big.Rat, loC, hiC bool) bool {
	cLo := v.Cmp(lo)
	if cLo < 0 || cLo == 0 && !loC {
		return false
	}
	cHi := v.Cmp(hi)
	if cHi > 0 || cHi == 0 && !hiC {
		return false
	}
	return true
}

func TestNonFinite(t *testing.T) {
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Shortest(x); err == nil {
			t.Errorf("Shortest(%v) 应返回错误", x)
		}
	}
}
