package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

func must(t *testing.T, s string) *api.Int {
	t.Helper()
	v, err := api.FromString(s)
	if err != nil {
		t.Fatalf("FromString(%q): %v", s, err)
	}
	return v
}

// 不变量1：八行表 + 生成用例，逐条核验 a==q*b+r、sign(r)==sign(a)、|r|<|b|。
func TestDivModContract(t *testing.T) {
	table := [][4]string{{"17", "5", "3", "2"}, {"-17", "5", "-3", "-2"},
		{"17", "-5", "-3", "2"}, {"-17", "-5", "3", "-2"},
		{"1000000000", "7", "142857142", "6"}, {"-1", "1000000000", "0", "-1"},
		{"0", "-5", "0", "0"}, {"7", "-10", "0", "7"}}
	for _, c := range table {
		q, r, err := must(t, c[0]).DivMod(must(t, c[1]))
		if err != nil || q.String() != c[2] || r.String() != c[3] {
			t.Errorf("DivMod(%s,%s)=(%s,%s),%v want (%s,%s)", c[0], c[1], q.String(), r.String(), err, c[2], c[3])
		}
	}
	as := []string{"-1000000001", "-999999999", "-17", "-1", "0", "1", "7", "123456789012345678"}
	bs := []string{"-1000000000", "-7", "-2", "3", "5", "999999999"}
	for _, sa := range as {
		for _, sb := range bs {
			a, b := must(t, sa), must(t, sb)
			q, r, err := a.DivMod(b)
			if err != nil {
				t.Fatalf("DivMod(%s,%s): %v", sa, sb, err)
			}
			if q.Mul(b).Add(r).String() != a.String() {
				t.Errorf("identity broken: %s != %s*%s+%s", sa, q, sb, r)
			}
			if r.Sign() != 0 && r.Sign() != a.Sign() {
				t.Errorf("sign(r)!=sign(a): %s/%s r=%s", sa, sb, r)
			}
			if r.Mul(r).Sub(b.Mul(b)).Sign() >= 0 {
				t.Errorf("|r|>=|b|: %s/%s r=%s", sa, sb, r)
			}
		}
	}
}

// 不变量2：字符串往返（含前导零、前导符号）。
func TestStringRoundTrip(t *testing.T) {
	table := [][2]string{{"0", "0"}, {"-0", "0"}, {"000", "0"}, {"000123", "123"},
		{"-0001230", "-1230"}, {"+42", "42"}, {"1000000000", "1000000000"},
		{"-98765432109876543210987654321", "-98765432109876543210987654321"}}
	for _, c := range table {
		v, err := api.FromString(c[0])
		if err != nil || v.String() != c[1] {
			t.Errorf("FromString(%q).String()=%q,%v want %q", c[0], v.String(), err, c[1])
		}
		w, _ := api.FromString(v.String())
		if w.String() != v.String() {
			t.Errorf("double roundtrip %q -> %q -> %q", c[0], v.String(), w.String())
		}
	}
}

// 不变量3：运算结果归一化——无 "-0"、无前导零。
func TestNormalized(t *testing.T) {
	zero := must(t, "0")
	table := []*api.Int{
		must(t, "5").Sub(must(t, "5")), must(t, "-5").Add(must(t, "5")),
		zero.Mul(must(t, "-123")), must(t, "-0").Add(zero),
		must(t, "1000000000").Sub(must(t, "1")), must(t, "999999999").Mul(must(t, "2")),
	}
	for _, v := range table {
		s := v.String()
		if s == "" || s == "-0" || (len(s) > 1 && s[0] == '0') || (len(s) > 2 && s[0] == '-' && s[1] == '0') {
			t.Errorf("not normalized: %q", s)
		}
		if s == "0" && v.Sign() != 0 {
			t.Errorf("zero with nonzero sign")
		}
	}
}

// 不变量4：三类可判定错误互不相同，被拒后状态不变且可继续使用。
func TestFailureAtomic(t *testing.T) {
	table := []struct {
		in   string
		want error
	}{{"", api.ErrInvalidDigit}, {"12a", api.ErrInvalidDigit}, {"1.5", api.ErrInvalidDigit},
		{"1-2", api.ErrBadSign}, {"-", api.ErrBadSign}, {"+", api.ErrBadSign}}
	for _, c := range table {
		if _, err := api.FromString(c.in); !errors.Is(err, c.want) {
			t.Errorf("FromString(%q) err=%v want %v", c.in, err, c.want)
		}
	}
	if _, _, err := must(t, "1").DivMod(must(t, "0")); !errors.Is(err, api.ErrDivByZero) {
		t.Errorf("DivMod by zero err=%v", err)
	}
	if api.ErrInvalidDigit == api.ErrBadSign || api.ErrBadSign == api.ErrDivByZero {
		t.Fatal("sentinel errors not distinct")
	}
	v := must(t, "42")
	before := v.String()
	_, _ = api.FromString("x")
	_, _, _ = v.DivMod(must(t, "0"))
	if v.String() != before || v.Add(must(t, "8")).String() != "50" {
		t.Error("state changed or unusable after rejected ops")
	}
}

// 并发只读：N 个 goroutine 的 String 必须逐字节相同。
func TestConcurrentRead(t *testing.T) {
	v := must(t, "123456789012345678901234567890")
	want := v.String()
	const n = 64
	got := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if v.Sign() != 1 || api.SelfCheck() != nil {
				t.Error("concurrent Sign/SelfCheck failed")
			}
			got[i] = v.String()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range got {
		if got[i] != want {
			t.Fatalf("goroutine %d got %q want %q", i, got[i], want)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
