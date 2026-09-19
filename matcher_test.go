package ontology

import "testing"

func TestPrefixMatchIsNotSubstring(t *testing.T) {
	cases := []struct {
		prefix string
		s      string
		want   bool
	}{
		{"user", "user", true},
		{"user", "user/42", true},
		{"user", "user:42", true},
		{"user", "user.42", true},
		{"user", "superuser", false},
		{"user", "userdata", false},
		{"user", "users", false},
		{"user", "use", false},
		{"", "anything", true},
		{"a/b", "a/b/c", true},
		{"a/b", "a/bc", false},
	}
	for _, c := range cases {
		if got := prefixMatch(c.prefix, c.s); got != c.want {
			t.Errorf("prefixMatch(%q,%q) = %v, want %v", c.prefix, c.s, got, c.want)
		}
	}
}

func TestMatcherPropertySet(t *testing.T) {
	all := newMatcher("user", nil)
	if !all.matches("user/1", "anything") {
		t.Error("empty property set must match every property")
	}
	if all.matches("other/1", "anything") {
		t.Error("entity prefix must still constrain an all-properties subscription")
	}

	m := newMatcher("user", []string{"name", "age"})
	if !m.matches("user/1", "name") {
		t.Error("exact property name must match")
	}
	if m.matches("user/1", "Name") || m.matches("user/1", "names") {
		t.Error("property matching must be exact, case-sensitive equality")
	}
	if m.matches("superuser", "name") {
		t.Error("prefix must not degrade into substring matching")
	}
}

func TestTargetsStableOrdering(t *testing.T) {
	d := New()
	defer d.Close()
	first, err := d.Subscribe("u", nil, Options{Capacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Subscribe("u", nil, Options{Capacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	third, err := d.Subscribe("other", nil, Options{Capacity: 1})
	if err != nil {
		t.Fatal(err)
	}

	got := d.Targets("u/1", "p")
	want := []int64{first.ID(), second.ID()}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	second.Unsubscribe()
	got = d.Targets("u/1", "p")
	if len(got) != 1 || got[0] != first.ID() {
		t.Fatalf("targets after unsubscribe = %v, want [%d]", got, first.ID())
	}
	if ids := d.Targets("other", "p"); len(ids) != 1 || ids[0] != third.ID() {
		t.Fatalf("other targets = %v", ids)
	}
}
