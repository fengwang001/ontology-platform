package doc

import (
	"bytes"
	"testing"
)

func TestCanonicalDeterministic(t *testing.T) {
	mk := func(order []string) Set {
		s := Set{}
		for _, k := range order {
			s[k] = Record{"b": Num(2), "a": Str("x"), "": Str("")}
		}
		return s
	}
	cases := []struct {
		name string
		a, b Set
	}{
		{"插入顺序不同", mk([]string{"k1", "k2", "k3"}), mk([]string{"k3", "k1", "k2"})},
		{"空键与空字段名", Set{"": Record{"": Str("")}}, Set{"": Record{"": Str("")}}},
	}
	for _, c := range cases {
		if !bytes.Equal(Canonical(c.a), Canonical(c.b)) {
			t.Errorf("%s: 规范化编码不一致", c.name)
		}
	}
}

func TestRoundtrip(t *testing.T) {
	cases := []Set{
		{},
		{"k": {}},
		{"": {"": Str("")}},
		{"r": {"a": Str("x"), "n": Num(3.5), "empty": Str("")}},
	}
	for i, s := range cases {
		got, n, err := Decode(Canonical(s))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if n != len(Canonical(s)) {
			t.Errorf("case %d: 消费字节数 %d != %d", i, n, len(Canonical(s)))
		}
		if !SetEqual(got, s) {
			t.Errorf("case %d: 往返不一致", i)
		}
	}
}

func TestEmptyValueVsAbsent(t *testing.T) {
	a := Record{"f": Str("")}
	b := Record{}
	if RecordEqual(a, b) {
		t.Error("空串值与字段不存在必须可区分")
	}
	if bytes.Equal(Canonical(Set{"k": a}), Canonical(Set{"k": b})) {
		t.Error("编码层面也必须可区分")
	}
}

func TestValueEquality(t *testing.T) {
	cases := []struct {
		a, b  Value
		equal bool
	}{
		{Str("1"), Str("1"), true},
		{Str("1"), Num(1), false},
		{Num(1), Num(1), true},
		{Str(""), Str(""), true},
	}
	for i, c := range cases {
		if Equal(c.a, c.b) != c.equal {
			t.Errorf("case %d: Equal(%v,%v)", i, c.a, c.b)
		}
	}
}

func TestDecodeTruncated(t *testing.T) {
	full := Canonical(Set{"k": {"a": Str("v")}})
	for i := 1; i < len(full); i++ {
		if _, _, err := Decode(full[:i]); err == nil {
			t.Fatalf("截断到 %d 字节未报错", i)
		}
	}
}

func SetEqual(a, b Set) bool {
	if len(a) != len(b) {
		return false
	}
	for k, ra := range a {
		rb, ok := b[k]
		if !ok || !RecordEqual(ra, rb) {
			return false
		}
	}
	return true
}
