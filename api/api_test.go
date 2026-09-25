package api

import (
	"errors"
	"math/rand"
	"strconv"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestParseKnownValues(t *testing.T) {
	cases := []struct{ in, want string }{
		{"3.14", "157/50"}, {"0.1", "1/10"}, {"-2.5", "-5/2"}, {"0.(3)", "1/3"},
		{"0.1(6)", "1/6"}, {"123", "123/1"}, {"0.(142857)", "1/7"}, {"-0.0", "0/1"},
	}
	p := New()
	for _, c := range cases {
		f, err := p.Parse(c.in)
		if err != nil || f.String() != c.want {
			t.Errorf("Parse(%q)=%v,%v want %s", c.in, f, err, c.want)
		}
	}
}

// digits 生成长 n 的随机数字串。
func digits(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('0' + r.Intn(10))
	}
	return string(b)
}

// TestParseAgainstBigRef 钉住不变量1：300 随机字面量须与 big.Rat 参照逐条相同（位数受限不溢出）。
func TestParseAgainstBigRef(t *testing.T) {
	rng, p := rand.New(rand.NewSource(42)), New()
	for n := 0; n < 300; n++ {
		a := 1 + rng.Intn(8)
		b := rng.Intn(15 - a)
		k := []int{0, 0, 1, 2, 3}[rng.Intn(5)]
		s := digits(rng, a)
		if b > 0 {
			s += "." + digits(rng, b)
		}
		if k > 0 {
			s += "(" + digits(rng, k) + ")"
		}
		if rng.Intn(2) == 0 {
			s = "-" + s
		}
		f, err := p.Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		want, err := refRat(s)
		if err != nil {
			t.Fatalf("ref(%q): %v", s, err)
		}
		if f.N != want.Num().Int64() || f.D != want.Denom().Int64() {
			t.Errorf("Parse(%q)=%s, big ref=%s", s, f, want.RatString())
		}
	}
}

func TestParseReducedNormalized(t *testing.T) {
	p := New()
	for _, s := range []string{"0", "-0", "0.0", "-0.0", "0.(0)", "-0.(0)", "0.00", "-0.00(0)"} {
		if f, err := p.Parse(s); err != nil || f.N != 0 || f.D != 1 {
			t.Errorf("Parse(%q)=%v,%v, want 0/1", s, f, err)
		}
	}
	for _, s := range []string{"3.14", "0.(3)", "0.1(6)", "-2.5", "123"} {
		f, _ := p.Parse(s)
		if f.D <= 0 || gcd(abs(f.N), f.D) != 1 {
			t.Errorf("Parse(%q)=%s not reduced/positive", s, f)
		}
	}
}

// TestRejectedInputsLeaveNoTrace 钉住不变量4：四类哨兵互异，拒绝不留痕、之后仍可用。
func TestRejectedInputsLeaveNoTrace(t *testing.T) {
	if len(map[error]bool{ErrEmpty: true, ErrIllegalChar: true, ErrSyntax: true, ErrOverflow: true}) != 4 {
		t.Fatal("sentinel errors not distinct")
	}
	cases := []struct {
		s string
		e error
	}{
		{"", ErrEmpty}, {" 1", ErrIllegalChar}, {"+1", ErrIllegalChar}, {"1a", ErrIllegalChar},
		{"1.2.3", ErrSyntax}, {"1.(2)(3)", ErrSyntax}, {"1.()", ErrSyntax},
		{"1.(2)3", ErrSyntax}, {"1-2", ErrSyntax}, {".5", ErrSyntax},
		{"-", ErrSyntax}, {"1.", ErrSyntax}, {"0.9999999999999999999", ErrOverflow},
	}
	p := New()
	for _, c := range cases {
		f, err := p.Parse(c.s)
		if !errors.Is(err, c.e) || f != nil {
			t.Errorf("Parse(%q): %v f=%v, want %v / nil Frac", c.s, err, f, c.e)
		}
		g, err := p.Parse("3.14") // 拒绝后状态不变、仍可继续正常使用
		if err != nil || g.String() != "157/50" {
			t.Fatalf("parser unusable after rejecting %q: %v %v", c.s, g, err)
		}
	}
}

// TestParseConcurrent：64 goroutine 并发 Parse 结果与串行一致，无 sleep，-race 干净。
func TestParseConcurrent(t *testing.T) {
	batch := []string{"3.14", "0.1", "-2.5", "0.(3)", "0.1(6)", "123",
		"0.(142857)", "-0.0", "1.(285714)", "1.0", "-0.(9)", "0.(0)"}
	p := New()
	serial := map[string]string{}
	for _, s := range batch {
		f, err := p.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		serial[s] = f.String()
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := false
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, s := range batch {
				f, err := p.Parse(s)
				st := f.String()
				mu.Lock()
				if err != nil || st != serial[s] ||
					st != strconv.FormatInt(f.N, 10)+"/"+strconv.FormatInt(f.D, 10) {
					failed = true
				}
				mu.Unlock()
			}
			_ = SelfCheck() // SelfCheck 与 Parse 并发
		}()
	}
	wg.Wait()
	if failed {
		t.Fatal("concurrent results diverged from serial")
	}
}
