package policy

import (
	"errors"
	"reflect"
	"testing"
)

func TestPolicy(t *testing.T) {
	t.Run("visibility", func(t *testing.T) {
		p := New("a", "b", "c")
		if err := p.Grant("r1", "a", "c"); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			role, col string
			want      bool
		}{
			{"r1", "a", true}, {"r1", "b", false}, {"r1", "c", true},
			{"r2", "a", false},
		}
		for _, tc := range cases {
			got, err := p.Visible(tc.role, tc.col)
			if err != nil || got != tc.want {
				t.Errorf("Visible(%q,%q)=%v,%v want %v", tc.role, tc.col, got, err, tc.want)
			}
		}
	})

	t.Run("unknown column", func(t *testing.T) {
		p := New("a")
		if err := p.Grant("r", "ghost"); !errors.Is(err, ErrUnknownColumn) {
			t.Fatalf("Grant err=%v want ErrUnknownColumn", err)
		}
		if _, err := p.Visible("r", "ghost"); !errors.Is(err, ErrUnknownColumn) {
			t.Fatalf("Visible err=%v want ErrUnknownColumn", err)
		}
	})

	t.Run("sets sorted", func(t *testing.T) {
		p := New("a", "b", "c")
		if err := p.Grant("r", "c", "a"); err != nil {
			t.Fatal(err)
		}
		if got := p.VisibleSet("r"); !reflect.DeepEqual(got, []string{"a", "c"}) {
			t.Errorf("VisibleSet=%v", got)
		}
		if got := p.HiddenSet("r"); !reflect.DeepEqual(got, []string{"b"}) {
			t.Errorf("HiddenSet=%v", got)
		}
		if got := p.HiddenSet("all"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
			t.Errorf("empty role HiddenSet=%v", got)
		}
	})

	t.Run("project removes invisible keys", func(t *testing.T) {
		p := New("id", "secret")
		if err := p.Grant("r", "id"); err != nil {
			t.Fatal(err)
		}
		row := map[string]string{"id": "7", "secret": "TOPSECRET"}
		got := p.Project("r", row)
		if !reflect.DeepEqual(got, map[string]string{"id": "7"}) {
			t.Fatalf("projected=%v", got)
		}
		if _, ok := got["secret"]; ok {
			t.Fatal("invisible key must be absent, distinguishible from empty/null value")
		}
		if row["secret"] != "TOPSECRET" {
			t.Fatal("source row must not be mutated")
		}
	})

	t.Run("empty and full grants", func(t *testing.T) {
		p := New("a", "b")
		if got := p.Project("none", map[string]string{"a": "1", "b": "2"}); len(got) != 0 {
			t.Fatalf("empty-role projection=%v", got)
		}
		if err := p.Grant("all", "a", "b"); err != nil {
			t.Fatal(err)
		}
		if got := p.Project("all", map[string]string{"a": "1", "b": "2"}); len(got) != 2 {
			t.Fatalf("full-role projection=%v", got)
		}
	})
}
