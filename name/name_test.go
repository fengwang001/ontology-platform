package name_test

import (
	"testing"

	"ontology/name"
)

func TestOps(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		add   string
		has   map[string]bool
		want  []string
	}{
		{"empty", nil, "", map[string]bool{"": true, "x": false}, []string{""}},
		{"basic", []string{"a", "b"}, "c", map[string]bool{"a": true, "z": false}, []string{"a", "b", "c"}},
		{"separator", []string{"a/b", `a\b`}, "a/b/c", map[string]bool{"a/b": true, `a\b`: true}, []string{"a/b", "a/b/c", `a\b`}},
		{"dup-add", []string{"a"}, "a", map[string]bool{"a": true}, []string{"a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns := name.New(c.names...)
			if !name.Valid(c.add) {
				t.Fatalf("Valid(%q) = false", c.add)
			}
			ns.Add(c.add)
			for s, want := range c.has {
				if got := ns.Has(s); got != want {
					t.Errorf("Has(%q) = %v, want %v", s, got, want)
				}
			}
			if got := ns.Snapshot(); !name.Equal(got, c.want) {
				t.Errorf("Snapshot = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRenameLocked(t *testing.T) {
	cases := []struct {
		name    string
		names   []string
		old     string
		new     string
		wantOK  bool
		wantSet []string
	}{
		{"ok", []string{"a", "b"}, "a", "c", true, []string{"b", "c"}},
		{"old-missing", []string{"a"}, "z", "c", false, []string{"a"}},
		{"new-exists", []string{"a", "b"}, "a", "b", false, []string{"a", "b"}},
		{"empty-name", []string{""}, "", "x", true, []string{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns := name.New(c.names...)
			ns.Lock()
			ok := ns.RenameLocked(c.old, c.new)
			ns.Unlock()
			if ok != c.wantOK {
				t.Fatalf("RenameLocked = %v, want %v", ok, c.wantOK)
			}
			if got := ns.Snapshot(); !name.Equal(got, c.wantSet) {
				t.Errorf("Snapshot = %v, want %v", got, c.wantSet)
			}
		})
	}
}
