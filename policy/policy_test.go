package policy

import (
	"reflect"
	"testing"
)

func TestPolicy(t *testing.T) {
	p := New([]string{"id", "name", "secret"})
	p.Grant("admin", "id", "name", "secret", "ghost")
	p.Grant("guest", "id", "id", "name")
	p.Grant("blind")

	cases := []struct {
		name    string
		role    string
		column  string
		visible bool
	}{
		{"admin sees secret", "admin", "secret", true},
		{"admin grant ghost ignored", "admin", "ghost", false},
		{"guest sees id", "guest", "id", true},
		{"guest blind to secret", "guest", "secret", false},
		{"unknown role sees nothing", "nobody", "id", false},
		{"empty-grant role blind", "blind", "id", false},
		{"unknown column not visible", "guest", "ghost", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Visible(tc.role, tc.column); got != tc.visible {
				t.Fatalf("Visible(%q,%q) = %v, want %v", tc.role, tc.column, got, tc.visible)
			}
		})
	}

	listCases := []struct {
		role string
		want []string
	}{
		{"admin", []string{"id", "name", "secret"}},
		{"guest", []string{"id", "name"}},
		{"blind", []string{}},
		{"nobody", []string{}},
	}
	for _, tc := range listCases {
		t.Run("list/"+tc.role, func(t *testing.T) {
			got := p.VisibleList(tc.role)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("VisibleList = %v, want %v", got, tc.want)
			}
		})
	}

	if got := p.HiddenList("guest"); !reflect.DeepEqual(got, []string{"secret"}) {
		t.Fatalf("HiddenList(guest) = %v, want [secret]", got)
	}
	if got := p.Universe(); !reflect.DeepEqual(got, []string{"id", "name", "secret"}) {
		t.Fatalf("Universe() = %v", got)
	}
	if !p.HasColumn("id") || p.HasColumn("ghost") {
		t.Fatalf("HasColumn wrong")
	}
	if !p.KnownRole("blind") || p.KnownRole("nobody") {
		t.Fatalf("KnownRole wrong")
	}
}
