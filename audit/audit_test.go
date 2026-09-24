package audit

import (
	"errors"
	"testing"

	"ontology/row"
)

func universe() []row.Row {
	return []row.Row{
		{Score: 1, ID: "a"},
		{Score: 1, ID: "b"},
		{Score: 2, ID: "c"},
		{Score: 3, ID: "d"},
	}
}

func TestVerify(t *testing.T) {
	u := universe()
	cases := []struct {
		name    string
		pages   [][]row.Row
		wantErr bool
	}{
		{"exact-one-page", [][]row.Row{u}, false},
		{"exact-split", [][]row.Row{u[:2], u[2:]}, false},
		{"empty", [][]row.Row{}, true},
		{"duplicate", [][]row.Row{u[:2], {u[1]}, u[2:]}, true},
		{"out-of-order", [][]row.Row{{u[1], u[0], u[2], u[3]}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify(tc.pages, universe())
			if tc.wantErr && !errors.Is(err, ErrMismatch) {
				t.Fatalf("got %v, want ErrMismatch", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}

func TestVerifyDetectsEachFailureKind(t *testing.T) {
	u := universe()
	cases := []struct {
		name  string
		pages [][]row.Row
		want  string
	}{
		{"dup", [][]row.Row{{u[0], u[0], u[1], u[2], u[3]}}, "twice"},
		{"missing", [][]row.Row{{u[0], u[2], u[3]}}, "missing"},
		{"order", [][]row.Row{{u[0], u[2], u[1], u[3]}}, "position"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify(tc.pages, u)
			if err == nil || !contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want to contain %q", err, tc.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
