package ontology

import "testing"

func TestPrefixIsNotSubstring(t *testing.T) {
	cases := []struct {
		prefix string
		path   string
		want   bool
	}{
		{"user", "user", true},
		{"user", "user/42", true},
		{"user", "user/42/name", true},
		{"user", "superuser", false},
		{"user", "superuser/1", false},
		{"user", "users", false},
		{"", "anything", true},
		{"", "", true},
		{"a/b", "a/b", true},
		{"a/b", "a/bc", false},
		{"a/b", "a/c", false},
	}
	for _, c := range cases {
		if got := prefixMatch(c.prefix, c.path); got != c.want {
			t.Errorf("prefixMatch(%q,%q)=%v want %v", c.prefix, c.path, got, c.want)
		}
	}
}

func TestAttributeSetMatching(t *testing.T) {
	d := New()
	all, _ := d.Subscribe("e", 10)
	named, _ := d.Subscribe("e", 10, WithAttributes("name", "age"))

	for _, attr := range []string{"name", "age", "email"} {
		d.Publish(Change{Entity: "e", Attribute: attr})
	}
	d.Close()
	if got := drain(all.C()); len(got) != 3 {
		t.Fatalf("empty-set subscriber got %d, want 3", len(got))
	}
	if got := drain(named.C()); len(got) != 2 {
		t.Fatalf("named subscriber got %d, want 2 (name,age)", len(got))
	}
	if ids := d.Targets("e", "nope"); len(ids) != 1 || ids[0] != all.ID() {
		t.Fatalf("targets for unmatched attr = %v, want only empty-set subscriber", ids)
	}
}

func TestTargetsSortedAndScoped(t *testing.T) {
	d := New()
	s1, _ := d.Subscribe("user", 1)
	s2, _ := d.Subscribe("user/1", 1)
	s3, _ := d.Subscribe("order", 1)

	got := d.Targets("user/1", "x")
	want := []uint64{s1.ID(), s2.ID()}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if ids := d.Targets("superuser", "x"); len(ids) != 0 {
		t.Fatalf("superuser matched prefix subscribers: %v", ids)
	}
	if ids := d.Targets("order/9", "x"); len(ids) != 1 || ids[0] != s3.ID() {
		t.Fatalf("order targets = %v", ids)
	}
}

func TestInvalidBufferRejected(t *testing.T) {
	d := New()
	if _, err := d.Subscribe("e", 0); err != ErrInvalidBuffer {
		t.Fatalf("buffer 0 err=%v, want ErrInvalidBuffer", err)
	}
	if _, err := d.Subscribe("e", -1); err != ErrInvalidBuffer {
		t.Fatalf("buffer -1 err=%v, want ErrInvalidBuffer", err)
	}
}
