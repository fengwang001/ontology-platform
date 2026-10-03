package rule

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		pattern []Pair
		l, w    int64
		mode    Mode
		wantErr bool
	}{
		{"ok", "a", []Pair{{"tenant", "*"}}, 1, 1, Shadow, false},
		{"empty id", "", []Pair{{"k", "v"}}, 1, 1, Enforce, true},
		{"empty pattern", "a", nil, 1, 1, Enforce, true},
		{"4 pairs", "a", []Pair{{"a", "1"}, {"b", "2"}, {"c", "3"}, {"d", "4"}}, 1, 1, Enforce, true},
		{"dup key", "a", []Pair{{"k", "1"}, {"k", "2"}}, 1, 1, Enforce, true},
		{"empty value", "a", []Pair{{"k", ""}}, 1, 1, Enforce, true},
		{"l zero", "a", []Pair{{"k", "*"}}, 0, 1, Enforce, true},
		{"w huge", "a", []Pair{{"k", "*"}}, 1, 1_000_000_001, Enforce, true},
		{"bad mode", "a", []Pair{{"k", "*"}}, 1, 1, Mode(9), true},
	}
	for _, c := range cases {
		_, err := ValidateRule(c.id, c.pattern, c.l, c.w, c.mode)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
	if err := ValidateDescriptor(Descriptor{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty desc err=%v", err)
	}
}

func TestMatch(t *testing.T) {
	r := &Rule{Pattern: []Pair{{"tenant", "*"}, {"route", "/pay"}}}
	if !r.Match(Descriptor{"tenant": "vip", "route": "/pay"}) {
		t.Fatal("should match wildcard+exact")
	}
	if r.Match(Descriptor{"route": "/pay"}) {
		t.Fatal("missing wildcard key must not match")
	}
	if r.Match(Descriptor{"tenant": "vip", "route": "/x"}) {
		t.Fatal("exact value differs must not match")
	}
	if !(&Rule{Pattern: []Pair{{"k", "*"}}}).Match(Descriptor{"k": "any", "extra": "1"}) {
		t.Fatal("extra descriptor keys are allowed")
	}
}

func TestSelect(t *testing.T) {
	desc := Descriptor{"tenant": "vip", "route": "/pay"}
	mk := func(id string, p []Pair) *Rule {
		r, err := ValidateRule(id, p, 10, 100, Enforce)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := mk("a", []Pair{{"tenant", "*"}, {"route", "/pay"}})
	b := mk("b", []Pair{{"tenant", "vip"}, {"route", "*"}})
	c := mk("c", []Pair{{"tenant", "vip"}, {"route", "/pay"}})

	// 同键集合各 1 个精确值：并列取 id 小者 a。
	got := Select([]*Rule{a, b})
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("tie break got=%v want [a]", got)
	}
	// c 有 2 个精确值：只选 c。
	got = Select([]*Rule{a, b, c})
	if len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("most specific got=%v want [c]", got)
	}

	// 不同键集合叠加：d 的键集合不同，二者都保留并按 id 排序。
	d := mk("d", []Pair{{"route", "/pay"}})
	var matched []*Rule
	for _, r := range []*Rule{a, b, d} {
		if r.Match(desc) {
			matched = append(matched, r)
		}
	}
	got = Select(matched)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "d" {
		t.Fatalf("different keysets overlay got=%v want [a d]", got)
	}
}

func TestPatternSig(t *testing.T) {
	s1 := PatternSig([]Pair{{"a", "1"}, {"b", "2"}})
	s2 := PatternSig([]Pair{{"b", "2"}, {"a", "1"}})
	s3 := PatternSig([]Pair{{"a", "2"}, {"b", "1"}})
	s4 := PatternSig([]Pair{{"a", "*"}, {"b", "2"}})
	if s1 != s2 {
		t.Fatal("order-insensitive signature required")
	}
	if s1 == s3 || s1 == s4 {
		t.Fatal("value/wildcard position must matter")
	}
}
