package dls

import (
	"errors"
	"testing"

	"ontology/role"
)

func TestResolveMerge(t *testing.T) {
	teamA := role.Term("team", "a")
	level59 := role.Range("level", 5, 9)

	tests := []struct {
		name        string
		groups      [][]role.Entry
		index       string
		wantErr     error
		docs        map[string]map[string]role.Value
		wantVisible map[string]bool
		wantFields  map[string]bool
	}{
		{
			name:    "empty M denied",
			groups:  [][]role.Entry{{{IndexPattern: "logs-*", Fields: role.FieldAuth{Unrestricted: true}}}},
			index:   "other",
			wantErr: role.ErrNoPerm,
		},
		{
			name: "single role filter plus fields",
			groups: [][]role.Entry{{{
				IndexPattern: "logs-*",
				Filter:       &teamA,
				Fields:       role.FieldAuth{Grant: []string{"team", "level"}},
			}}},
			index: "logs-1",
			docs: map[string]map[string]role.Value{
				"d1": {"team": "a", "level": int64(3), "secret": "k1"},
				"d2": {"team": "b", "level": int64(5), "secret": "k2"},
			},
			wantVisible: map[string]bool{"d1": true, "d2": false},
			wantFields:  map[string]bool{"team": true, "level": true, "secret": false},
		},
		{
			name: "union of filters R1 or R2",
			groups: [][]role.Entry{
				{{IndexPattern: "logs-*", Filter: &teamA, Fields: role.FieldAuth{Grant: []string{"team", "level"}}}},
				{{IndexPattern: "logs-1", Filter: &level59, Fields: role.FieldAuth{Grant: []string{"*"}, Except: []string{"secret"}}}},
			},
			index: "logs-1",
			docs: map[string]map[string]role.Value{
				"d1": {"team": "a", "level": int64(3), "secret": "k1"},
				"d2": {"team": "b", "level": int64(5), "secret": "k2"},
				"d3": {"team": "a", "level": int64(9)},
			},
			wantVisible: map[string]bool{"d1": true, "d2": true, "d3": true},
			wantFields:  map[string]bool{"team": true, "level": true, "secret": false, "other": true},
		},
		{
			name: "one nil filter opens all docs, fields unioned independently",
			groups: [][]role.Entry{
				{{IndexPattern: "logs-*", Filter: &teamA, Fields: role.FieldAuth{Grant: []string{"team", "level"}}}},
				{{IndexPattern: "logs-1", Fields: role.FieldAuth{Grant: []string{"secret"}}}},
			},
			index:       "logs-1",
			docs:        map[string]map[string]role.Value{"d2": {"team": "b", "secret": "k2"}, "d3": {"team": "a"}},
			wantVisible: map[string]bool{"d2": true, "d3": true},
			wantFields:  map[string]bool{"team": true, "level": true, "secret": true, "nope": false},
		},
		{
			name:        "unrestricted fields open every field",
			groups:      [][]role.Entry{{{IndexPattern: "*", Filter: &teamA, Fields: role.FieldAuth{Unrestricted: true}}}},
			index:       "logs-9",
			docs:        map[string]map[string]role.Value{"d": {"x": int64(1), "y": "z"}},
			wantVisible: map[string]bool{"d": false},
			wantFields:  map[string]bool{"x": true, "y": true, "anything": true},
		},
		{
			name: "except only subtracts within its own entry",
			groups: [][]role.Entry{
				{{IndexPattern: "logs-1", Fields: role.FieldAuth{Grant: []string{"*"}, Except: []string{"secret"}}}},
				{{IndexPattern: "logs-1", Fields: role.FieldAuth{Grant: []string{"secret"}}}},
			},
			index:       "logs-1",
			docs:        map[string]map[string]role.Value{"d": {"secret": "k", "team": "a"}},
			wantVisible: map[string]bool{"d": true},
			wantFields:  map[string]bool{"secret": true, "team": true, "zzz": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			view, err := Resolve(tc.groups, tc.index)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			for id, fields := range tc.docs {
				if got := view.DocVisible(fields); got != tc.wantVisible[id] {
					t.Errorf("DocVisible(%s) = %v, want %v", id, got, tc.wantVisible[id])
				}
			}
			for field, want := range tc.wantFields {
				if got := view.FieldVisible(field); got != want {
					t.Errorf("FieldVisible(%s) = %v, want %v", field, got, want)
				}
			}
		})
	}
}

func TestPatternBoundaries(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"logs-*", "logs-1", true},
		{"logs-*", "logs-", true},
		{"logs-*", "log", false},
		{"logs-*", "xlogs-1", false},
		{"*", "anything", true},
		{"*", "", true},
		{"logs-1", "logs-1", true},
		{"logs-1", "logs-2", false},
		{"", "a", false},
	}
	for _, tc := range tests {
		if got := role.MatchPattern(tc.pattern, tc.name); got != tc.want {
			t.Errorf("MatchPattern(%q,%q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}

	// 非法模式（非末尾星号 / 双星星）在条目校验阶段即拒绝。
	bad := []role.Entry{
		{IndexPattern: "a*b", Fields: role.FieldAuth{Unrestricted: true}},
		{IndexPattern: "a**", Fields: role.FieldAuth{Unrestricted: true}},
	}
	for _, entry := range bad {
		if err := func() error {
			s := role.NewStore()
			return s.PutRole("R", []role.Entry{entry})
		}(); !errors.Is(err, role.ErrInvalid) {
			t.Errorf("bad pattern %q accepted: %v", entry.IndexPattern, err)
		}
	}
}

func TestTouchedBoundedByUserEntries(t *testing.T) {
	groups := [][]role.Entry{
		{{IndexPattern: "no-1", Fields: role.FieldAuth{Unrestricted: true}},
			{IndexPattern: "no-2", Fields: role.FieldAuth{Unrestricted: true}}},
		{{IndexPattern: "ix", Fields: role.FieldAuth{Unrestricted: true}}},
	}
	view, err := Resolve(groups, "ix")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	if view.touched != total || view.touched > total {
		t.Fatalf("touched = %d, want %d", view.touched, total)
	}
}

func TestEvalMissingAndMismatch(t *testing.T) {
	doc := map[string]role.Value{"level": int64(5), "team": "a"}
	if role.Eval(role.Term("missing", "x"), doc) {
		t.Error("Term on missing field must be false")
	}
	if role.Eval(role.Range("team", 1, 9), doc) {
		t.Error("Range on string field must be false")
	}
	if !role.Eval(role.Not(role.Term("missing", "x")), doc) {
		t.Error("Not(Term missing) must be true")
	}
	if role.Eval(role.Range("level", 6, 10), doc) {
		t.Error("closed range 5..10 must exclude 5")
	}
	if !role.Eval(role.Range("level", 5, 9), doc) {
		t.Error("closed range must include bounds")
	}
}
