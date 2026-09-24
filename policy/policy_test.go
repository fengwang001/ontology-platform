package policy

import (
	"reflect"
	"testing"
)

func TestPolicy(t *testing.T) {
	p := New()
	p.Grant("analyst", "name", "dept")
	p.Grant("analyst", "dept") // duplicate grant is idempotent
	p.Grant("empty")

	cases := []struct {
		name    string
		role    string
		col     string
		known   bool
		visible bool
		set     []string
	}{
		{"visible column", "analyst", "name", true, true, []string{"dept", "name"}},
		{"granted once more", "analyst", "dept", true, true, []string{"dept", "name"}},
		{"hidden column", "analyst", "secret", true, false, []string{"dept", "name"}},
		{"empty set role", "empty", "name", true, false, []string{}},
		{"unknown role", "ghost", "name", false, false, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Known(tc.role); got != tc.known {
				t.Errorf("Known(%q) = %v, want %v", tc.role, got, tc.known)
			}
			if got := p.Visible(tc.role, tc.col); got != tc.visible {
				t.Errorf("Visible(%q, %q) = %v, want %v", tc.role, tc.col, got, tc.visible)
			}
			if got := p.VisibleSet(tc.role); !reflect.DeepEqual(got, tc.set) {
				t.Errorf("VisibleSet(%q) = %v, want %v", tc.role, got, tc.set)
			}
		})
	}
}

func TestVisibleSetSorted(t *testing.T) {
	p := New()
	p.Grant("r", "zeta", "alpha", "mid")
	want := []string{"alpha", "mid", "zeta"}
	if got := p.VisibleSet("r"); !reflect.DeepEqual(got, want) {
		t.Errorf("VisibleSet = %v, want sorted %v", got, want)
	}
}
