package decay

import (
	"errors"
	"testing"
)

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name                 string
		delta, num, den, max int64
	}{
		{"delta=0", 0, 1, 2, 100},
		{"delta>1e6", 1_000_001, 1, 2, 100},
		{"num=0", 10, 0, 2, 100},
		{"num=den", 10, 2, 2, 100},
		{"num>den", 10, 3, 2, 100},
		{"den>1000", 10, 1, 1001, 100},
		{"pmax=0", 10, 1, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.delta, c.num, c.den, c.max); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("New(%d,%d,%d,%d) err=%v, want ErrInvalidParam", c.delta, c.num, c.den, c.max, err)
			}
		})
	}
	if _, err := New(1_000_000, 999, 1000, 1); err != nil {
		t.Fatalf("valid boundary params rejected: %v", err)
	}
}

func TestMaxSteps(t *testing.T) {
	// 10^9 按 1/2 逐步减半，第 30 步到 0。
	d, err := New(1, 1, 2, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if d.MaxSteps() != 30 {
		t.Fatalf("MaxSteps=%d, want 30", d.MaxSteps())
	}
}

func TestStepwiseVsOneShot(t *testing.T) {
	// 逐步取整：13 -> ⌊13·3/4⌋=9 -> ⌊9·3/4⌋=6；
	// 一次取整 ⌊13·9/16⌋=7，二者必须可区分。
	d, err := New(10, 3, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Decay(13, 2); got != 6 {
		t.Fatalf("Decay(13,2)=%d, want 6 (stepwise)", got)
	}
	if oneShot := 13 * 9 / 16; oneShot != 7 {
		t.Fatalf("test premise broken: one-shot=%d, want 7", oneShot)
	}
}

func TestDecayMonotoneAndZero(t *testing.T) {
	d, err := New(5, 3, 4, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	prev := int64(1_000_000_000)
	for k := int64(1); k <= int64(d.MaxSteps()); k++ {
		p := d.Decay(1_000_000_000, k)
		if p > prev {
			t.Fatalf("not monotone: k=%d p=%d prev=%d", k, p, prev)
		}
		prev = p
	}
	if prev != 0 {
		t.Fatalf("after Z steps p=%d, want 0", prev)
	}
}

// 乘法步数上界：与间隔步数 k 无关，空闲 10^3 步与 10^9 步对照。
// 取 1/2 系数使 Z=30，两档 k 均远超 Z。
func TestMulStepsBound(t *testing.T) {
	d, err := New(1, 1, 2, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	z := d.MaxSteps()
	t.Logf("Z=%d", z)

	d.Decay(1_000_000_000, 1_000)
	m1 := d.MulSteps()
	d.Decay(1_000_000_000, 1_000_000_000)
	m2 := d.MulSteps()
	t.Logf("Decay: k=1e3 mulSteps=%d, k=1e9 mulSteps=%d, Z+1=%d", m1, m2, z+1)
	if m1 != m2 {
		t.Fatalf("mulSteps depends on k: %d vs %d", m1, m2)
	}
	if m2 > z+1 {
		t.Fatalf("Decay mulSteps=%d exceeds Z+1=%d", m2, z+1)
	}

	d.StepsBelow(1_000_000_000, 1)
	m3 := d.MulSteps()
	t.Logf("StepsBelow: mulSteps=%d, Z+1=%d", m3, z+1)
	if m3 > z+1 {
		t.Fatalf("StepsBelow mulSteps=%d exceeds Z+1=%d", m3, z+1)
	}
}

func TestStepsBelow(t *testing.T) {
	d, err := New(10, 1, 2, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		p, pr int64
		want  int64
	}{
		{1600, 800, 2}, // 1600->800(恰等不解除)->400
		{1599, 800, 1}, // 1599->799
		{100, 800, 1},  // 已低于 Pr 仍取最小正整数 1
		{0, 1, 1},
	}
	for _, c := range cases {
		if got := d.StepsBelow(c.p, c.pr); got != c.want {
			t.Fatalf("StepsBelow(%d,%d)=%d, want %d", c.p, c.pr, got, c.want)
		}
	}
}
