package authz

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want Grants
	}{
		{"empty means none", nil, nil},
		{"empty prefix all", []string{""}, Grants{""}},
		{"dedup", []string{"a", "a", "b"}, Grants{"a", "b"}},
		{"drop covered", []string{"ab", "a", "ac", "b"}, Grants{"a", "b"}},
		{"kept distinct", []string{"ab", "ac", "b"}, Grants{"ab", "ac", "b"}},
		{"ff edge", []string{"x\xff", "x"}, Grants{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.in); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Normalize(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestVisible(t *testing.T) {
	g := Normalize([]string{"b/", "d/"})
	vis := []string{"b/", "b/1", "d/x/y"}
	invis := []string{"", "a", "b", "c", "d", "e"}
	for _, k := range vis {
		if !g.Visible(k) {
			t.Fatalf("%q should be visible", k)
		}
	}
	for _, k := range invis {
		if g.Visible(k) {
			t.Fatalf("%q should be invisible", k)
		}
	}
	if !Normalize([]string{""}).Visible("anything\xff") {
		t.Fatal("empty grant should reveal everything")
	}
}

func TestNextVisible(t *testing.T) {
	g := Normalize([]string{"b/", "d/"})
	cases := []struct {
		key  string
		want string
		ok   bool
	}{
		{"", "b/", true},
		{"a", "b/", true},
		{"b", "b/", true},
		{"b/0", "b/0", true}, // 已在区间
		{"b/zzz", "b/zzz", true},
		{"c", "d/", true},
		{"d/9", "d/9", true},
		{"e", "", false},
		{"\xff", "", false},
	}
	for _, c := range cases {
		got, ok := g.NextVisible(c.key)
		if ok != c.ok || ok && got != c.want {
			t.Fatalf("NextVisible(%q)=%q,%v want %q,%v", c.key, got, ok, c.want, c.ok)
		}
	}

	all := Normalize([]string{""})
	if got, ok := all.NextVisible("z"); !ok || got != "z" {
		t.Fatalf("all grant NextVisible = %q,%v", got, ok)
	}
}
