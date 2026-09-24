package audit

import (
	"errors"
	"testing"

	"ontology/row"
)

func rows(ids ...string) []row.Row {
	out := make([]row.Row, len(ids))
	for i, id := range ids {
		out[i] = row.Row{Key: float64(i), ID: id}
	}
	return out
}

func TestExactlyOnce(t *testing.T) {
	full := rows("a", "b", "c", "d")
	cases := []struct {
		name  string
		pages [][]row.Row
		want  []row.Row
		err   error
	}{
		{"exact-partition", [][]row.Row{full[:2], full[2:]}, full, nil},
		{"single-page", [][]row.Row{full}, full, nil},
		{"duplicate-row", [][]row.Row{full[:2], full[1:]}, full, ErrDuplicate},
		{"missing-row", [][]row.Row{full[:2]}, full, ErrMissing},
		{"extra-row", [][]row.Row{full, rows("z")}, full, ErrMissing},
		{"wrong-order", [][]row.Row{{full[1], full[0], full[2], full[3]}}, full, ErrOrder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ExactlyOnce(tc.pages, tc.want)
			if !errors.Is(err, tc.err) {
				t.Fatalf("got %v, want %v", err, tc.err)
			}
		})
	}
}

func TestNoRepeats(t *testing.T) {
	cases := []struct {
		name  string
		pages [][]row.Row
		err   error
	}{
		{"clean", [][]row.Row{rows("a", "b"), rows("c")}, nil},
		{"dup-across-pages", [][]row.Row{rows("a"), rows("a")}, ErrDuplicate},
		{"dup-within-page", [][]row.Row{rows("a", "a")}, ErrDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := NoRepeats(tc.pages); !errors.Is(err, tc.err) {
				t.Fatalf("got %v, want %v", err, tc.err)
			}
		})
	}
}
