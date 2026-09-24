package name_test

import (
	"errors"
	"testing"

	"ontology/name"
)

func TestNamespace(t *testing.T) {
	type tc struct {
		desc    string
		initial []string
		ops     func(ns *name.Namespace) error
		wantErr bool
		want    []string
	}
	cases := []tc{
		{"add and remove", []string{"a"}, func(ns *name.Namespace) error {
			if err := ns.Add("b"); err != nil {
				return err
			}
			return ns.Remove("a")
		}, false, []string{"b"}},
		{"dup add", []string{"a"}, func(ns *name.Namespace) error { return ns.Add("a") }, true, []string{"a"}},
		{"missing remove", nil, func(ns *name.Namespace) error { return ns.Remove("x") }, true, nil},
		{"invalid add", nil, func(ns *name.Namespace) error { return ns.Add("a\x00b") }, true, nil},
		{"invalid remove", []string{"a\x00"}, func(ns *name.Namespace) error { return ns.Remove("a\x00") }, true, []string{"a\x00"}},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			ns := name.New(c.initial...)
			err := c.ops(ns)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			got := ns.Snapshot()
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}

func TestValidAndSetEquality(t *testing.T) {
	type vc struct {
		in   string
		want bool
	}
	validCases := []vc{
		{"", true}, {"a", true}, {"a/b", true}, {"a\\b", true},
		{"/", true}, {"a\x00", false}, {"\x00", false},
	}
	for _, c := range validCases {
		if got := name.Valid(c.in); got != c.want {
			t.Fatalf("Valid(%q)=%v want %v", c.in, got, c.want)
		}
	}
	a := name.New("x", "y", "")
	b := name.New("", "y", "x")
	if !name.EqualAsSet(a, b) {
		t.Fatal("same sets in different order must be equal")
	}
	b.Add("z")
	if name.EqualAsSet(a, b) {
		t.Fatal("different sets must not be equal")
	}
}

// err 是测试辅助：把 bool 转成 error 以便 errors.Is 演示路径。
func TestInvalidSentinel(t *testing.T) {
	ns := name.New()
	if !errors.Is(ns.Add("x\x00"), name.ErrInvalidName) {
		t.Fatal("invalid name must be errors.Is ErrInvalidName")
	}
}
