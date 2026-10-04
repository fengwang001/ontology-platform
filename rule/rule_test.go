package rule

import (
	"errors"
	"testing"
)

func TestPutDropVersion(t *testing.T) {
	s := NewSet()
	if got := s.RV(); got != 0 {
		t.Fatalf("initial rv = %d, want 0", got)
	}
	cases := []struct {
		name string
		fn   func() error
		want error
		rv   int64
	}{
		{"put r1", func() error { return s.Put(Rule{"r1", "amt", 0, 100, Block}) }, nil, 1},
		{"put r2", func() error { return s.Put(Rule{"r2", "qty", 1, 10, Warn}) }, nil, 2},
		{"overwrite r1 bumps rv", func() error { return s.Put(Rule{"r1", "amt", 0, 200, Block}) }, nil, 3},
		{"same-content overwrite still bumps", func() error { return s.Put(Rule{"r1", "amt", 0, 200, Block}) }, nil, 4},
		{"drop r1", func() error { return s.Drop("r1") }, nil, 5},
		{"drop missing", func() error { return s.Drop("r1") }, ErrRuleNotFound, 5},
		{"put empty id", func() error { return s.Put(Rule{"", "f", 0, 1, Block}) }, ErrInvalidParam, 5},
		{"put empty field", func() error { return s.Put(Rule{"x", "", 0, 1, Block}) }, ErrInvalidParam, 5},
		{"put lo>hi", func() error { return s.Put(Rule{"x", "f", 2, 1, Block}) }, ErrInvalidParam, 5},
		{"put bad severity", func() error { return s.Put(Rule{"x", "f", 0, 1, Severity(9)}) }, ErrInvalidParam, 5},
		{"drop empty id", func() error { return s.Drop("") }, ErrInvalidParam, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := s.RV(); got != tc.rv {
				t.Fatalf("rv = %d, want %d", got, tc.rv)
			}
		})
	}
}

func TestEval(t *testing.T) {
	s := NewSet()
	mustPut := func(r Rule) {
		t.Helper()
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	mustPut(Rule{"r1", "amt", 0, 100, Block})
	mustPut(Rule{"r2", "qty", 1, 10, Warn})
	rv := s.RV()

	cases := []struct {
		name   string
		fields map[string]int64
		viol   []string
		block  bool
	}{
		{"clean", map[string]int64{"amt": 50, "qty": 5}, nil, false},
		{"warn only", map[string]int64{"amt": 50, "qty": 0}, []string{"r2"}, false},
		{"block only", map[string]int64{"amt": 150, "qty": 1}, []string{"r1"}, true},
		{"missing field violates both", map[string]int64{"amt": -1}, []string{"r1", "r2"}, true},
		{"boundary inclusive", map[string]int64{"amt": 0, "qty": 10}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := s.Eval(tc.fields)
			if v.RV != rv {
				t.Fatalf("verdict rv = %d, want %d", v.RV, rv)
			}
			if v.HasBlock != tc.block {
				t.Fatalf("HasBlock = %v, want %v", v.HasBlock, tc.block)
			}
			if !equalStrings(v.Violations, tc.viol) {
				t.Fatalf("violations = %v, want %v (must be ascending)", v.Violations, tc.viol)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
