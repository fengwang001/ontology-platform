package rule

import (
	"reflect"
	"testing"
)

func TestMatch(t *testing.T) {
	desc := map[string]string{"tenant": "vip", "route": "/pay"}
	cases := []struct {
		name    string
		pattern []KV
		want    bool
	}{
		{"exact hit", []KV{{"tenant", "vip"}}, true},
		{"exact miss", []KV{{"tenant", "free"}}, false},
		{"wildcard present", []KV{{"route", "*"}}, true},
		{"wildcard absent", []KV{{"region", "*"}}, false},
		{"mixed hit", []KV{{"tenant", "vip"}, {"route", "*"}}, true},
		{"mixed miss", []KV{{"tenant", "*"}, {"route", "/refund"}}, false},
	}
	for _, tc := range cases {
		if got := NewPattern(tc.pattern).Match(desc); got != tc.want {
			t.Errorf("%s: Match = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSelect(t *testing.T) {
	mk := func(id string, pairs ...KV) Rule {
		return Rule{ID: id, Pattern: NewPattern(pairs)}
	}
	cases := []struct {
		name  string
		rules []Rule
		want  []string
	}{
		{
			"more exact pairs wins within a key set",
			[]Rule{
				mk("a", KV{"tenant", "*"}, KV{"route", "/pay"}),
				mk("b", KV{"tenant", "vip"}, KV{"route", "*"}),
				mk("c", KV{"tenant", "vip"}, KV{"route", "/pay"}),
			},
			[]string{"c"},
		},
		{
			"tie on exact count takes smallest id",
			[]Rule{
				mk("b", KV{"tenant", "vip"}, KV{"route", "*"}),
				mk("a", KV{"tenant", "*"}, KV{"route", "/pay"}),
			},
			[]string{"a"},
		},
		{
			"different key sets stack",
			[]Rule{
				mk("x", KV{"tenant", "vip"}),
				mk("y", KV{"route", "/pay"}),
				mk("z", KV{"tenant", "*"}),
			},
			[]string{"x", "y"},
		},
		{
			"winners sorted by id across key sets",
			[]Rule{
				mk("m", KV{"route", "*"}),
				mk("a", KV{"tenant", "*"}),
			},
			[]string{"a", "m"},
		},
	}
	for _, tc := range cases {
		got := Select(tc.rules)
		ids := make([]string, len(got))
		for i, r := range got {
			ids[i] = r.ID
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Errorf("%s: Select = %v, want %v", tc.name, ids, tc.want)
		}
	}
}

func TestSignatureAndValuesKey(t *testing.T) {
	p1 := NewPattern([]KV{{"route", "*"}, {"tenant", "vip"}})
	p2 := NewPattern([]KV{{"tenant", "vip"}, {"route", "*"}})
	if p1.Signature() != p2.Signature() {
		t.Error("same pair set in different order must share a signature")
	}
	p3 := NewPattern([]KV{{"tenant", "*"}, {"route", "*"}})
	if p1.Signature() == p3.Signature() {
		t.Error("different wildcard positions must differ in signature")
	}
	desc := map[string]string{"tenant": "acme", "route": "/pay"}
	if got := p1.ValuesKey(desc); got != p2.ValuesKey(desc) {
		t.Error("values key must be independent of input pair order")
	}
}
