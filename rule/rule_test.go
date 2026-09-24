package rule

import (
	"errors"
	"testing"
)

func TestSplitPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a", []string{"a"}},
		{"a.b.c", []string{"a", "b", "c"}},
		{`a\.b`, []string{"a.b"}},
		{`a\.b.c`, []string{"a.b", "c"}},
		{`x\.y\.z`, []string{"x.y.z"}},
	}
	for _, tc := range cases {
		got := SplitPath(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("SplitPath(%q)=%v want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("SplitPath(%q)=%v want %v", tc.in, got, tc.want)
			}
		}
	}
}

func TestCompileAndLookup(t *testing.T) {
	rules := []Rule{
		{Name: "r1", Path: "user.password", Method: Replace, Replace: "***"},
		{Name: "r2", Path: `a\.b`, Method: Hash},
		{Name: "r3", Path: "a.b", Method: Truncate, Keep: 2},
		{Name: "r4", Path: "arr.0.k", Method: Hash},
		{Name: "v1", Value: "SECRET", Method: Replace, Replace: "REDACTED"},
	}
	s, err := Compile(rules)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		name string
		segs []string
		want Method
	}{
		{"nested path", []string{"user", "password"}, Replace},
		{"literal dotted key", []string{"a.b"}, Hash},
		{"escaped differs from nested", []string{"a", "b"}, Truncate},
		{"array index segment", []string{"arr", "0", "k"}, Hash},
		{"missing path", []string{"nope", "x"}, 0},
		{"mid is not a map key", []string{"user", "password", "deeper"}, 0},
	}
	for _, tc := range cases {
		got := s.Lookup(tc.segs)
		var m Method
		if got != nil {
			m = got.Method
		}
		if m != tc.want {
			t.Fatalf("%s: method=%v want %v", tc.name, m, tc.want)
		}
	}
	if a := s.LookupValue("SECRET"); a == nil || a.Replace != "REDACTED" {
		t.Fatalf("value rule lookup failed")
	}
	if s.LookupValue("other") != nil {
		t.Fatalf("unexpected value match")
	}
}

func TestCompileConflict(t *testing.T) {
	cases := []struct {
		name  string
		rules []Rule
	}{
		{"same path", []Rule{
			{Name: "a", Path: "p", Method: Replace},
			{Name: "b", Path: "p", Method: Hash},
		}},
		{"same value", []Rule{
			{Name: "a", Value: "V", Method: Hash},
			{Name: "b", Value: "V", Method: Truncate, Keep: 1},
		}},
	}
	for _, tc := range cases {
		_, err := Compile(tc.rules)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: err=%v want ErrConflict (must name both rules: %v)",
				tc.name, err, err)
		}
	}
	if _, err := Compile([]Rule{{Name: "ok", Path: "p", Method: Hash},
		{Name: "ok2", Path: "p", Method: Hash}}); err != nil {
		t.Fatalf("same method must not conflict: %v", err)
	}
}

func TestMatchComplexity(t *testing.T) {
	mk := func(n int) []Rule {
		var rs []Rule
		for i := 0; i < n; i++ {
			rs = append(rs, Rule{Name: "r", Path: "user.field" + itoa(i), Method: Hash})
		}
		return rs
	}
	segs := [][]string{{"user"}, {"user", "name"}, {"meta"}, {"x"}, {"y"}}
	const records = 100000
	for _, n := range []int{1, 100} {
		s, _ := Compile(mk(n))
		for i := 0; i < records; i++ {
			for _, p := range segs {
				s.Lookup(p)
			}
		}
		bound := uint64(records * (len(segs) + 8))
		if s.MatchCount > bound {
			t.Fatalf("rules=%d matches=%d > bound=%d", n, s.MatchCount, bound)
		}
		if n == 100 && s.MatchCount != uint64(records*len(segs)) {
			t.Fatalf("match count must be independent of rule count: %d", s.MatchCount)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
