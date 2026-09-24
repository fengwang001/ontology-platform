package predicate

import (
	"reflect"
	"testing"
)

func TestNodeCount(t *testing.T) {
	cmp := Compare{Column: "a", Value: "1"}
	cases := []struct {
		name string
		pred Pred
		want int
	}{
		{"nil means no filter", nil, 0},
		{"const", Const{true}, 1},
		{"compare", cmp, 1},
		{"is null", IsNull{Column: "a"}, 1},
		{"not", Not{Inner: cmp}, 2},
		{"and", And{L: cmp, R: cmp}, 3},
		{"or nested", Or{L: Or{L: cmp, R: cmp}, R: Not{Inner: cmp}}, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NodeCount(tc.pred); got != tc.want {
				t.Errorf("NodeCount = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFold(t *testing.T) {
	cmpA := Compare{Column: "a", Value: "1"}
	cmpB := Compare{Column: "b", Value: "2"}
	cases := []struct {
		name string
		in   Pred
		want Pred
	}{
		{"not true", Not{Inner: Const{true}}, Const{false}},
		{"not false", Not{Inner: Const{false}}, Const{true}},
		{"or true absorbs left", Or{L: Const{true}, R: cmpA}, Const{true}},
		{"or true absorbs right", Or{L: cmpA, R: Const{true}}, Const{true}},
		{"or false drops branch", Or{L: Const{false}, R: cmpA}, cmpA},
		{"and false absorbs", And{L: cmpA, R: Const{false}}, Const{false}},
		{"and true drops branch", And{L: Const{true}, R: cmpA}, cmpA},
		{"and both kept", And{L: cmpA, R: cmpB}, And{L: cmpA, R: cmpB}},
		{"nested fold", Or{L: And{L: cmpA, R: Const{false}}, R: Const{false}}, Const{false}},
		{"leaf untouched", cmpA, cmpA},
		{"is null untouched", IsNull{Column: "s"}, IsNull{Column: "s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Fold(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Fold = %#v, want %#v", got, tc.want)
			}
		})
	}
}
