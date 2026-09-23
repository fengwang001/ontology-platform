package graph

import (
	"errors"
	"testing"

	"ontology/rng"
	"ontology/ver"
)

func TestAddVersion(t *testing.T) {
	cases := []struct {
		name    string
		pkg, v  string
		wantErr error
	}{
		{"ok", "a", "1.0.0", nil},
		{"second version", "a", "2.0.0", nil},
		{"other package", "b", "1.0.0", nil},
		{"duplicate", "a", "1.0.0", ErrDuplicateVersion},
		{"bad version", "c", "1.0", ver.ErrInvalidVersion},
	}
	g := New()
	for _, c := range cases {
		err := g.AddVersion(c.pkg, c.v)
		if c.wantErr == nil && err != nil {
			t.Errorf("%s: AddVersion(%s,%s) err=%v", c.name, c.pkg, c.v, err)
		}
		if c.wantErr != nil && !errors.Is(err, c.wantErr) {
			t.Errorf("%s: AddVersion(%s,%s) err=%v, want %v", c.name, c.pkg, c.v, err, c.wantErr)
		}
	}
	if got := len(g.Versions("a")); got != 2 {
		t.Errorf("len(Versions(a))=%d, want 2 (failed duplicate must not stick)", got)
	}
}

func TestAddConstraint(t *testing.T) {
	g := New()
	if err := g.AddVersion("a", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name           string
		pkg, v, target string
		con            string
		wantErr        error
	}{
		{"ok", "a", "1.0.0", "b", ">=1.0.0", nil},
		{"bad constraint", "a", "1.0.0", "b", ">>1.0.0", rng.ErrInvalidConstraint},
		{"unknown source pkg", "zz", "1.0.0", "b", ">=1.0.0", ErrUnknownVersion},
		{"unknown source ver", "a", "9.9.9", "b", ">=1.0.0", ErrUnknownVersion},
		{"unregistered target ok", "a", "1.0.0", "not-yet", ">=1.0.0", nil},
	}
	for _, c := range cases {
		err := g.AddConstraint(c.pkg, c.v, c.target, c.con)
		if c.wantErr == nil && err != nil {
			t.Errorf("%s: err=%v", c.name, err)
		}
		if c.wantErr != nil && !errors.Is(err, c.wantErr) {
			t.Errorf("%s: err=%v, want %v", c.name, err, c.wantErr)
		}
	}
	if got := len(g.Dependencies("a", "1.0.0")); got != 2 {
		t.Errorf("len(Dependencies)=%d, want 2 (failed adds must not stick)", got)
	}
}

func TestVersionsSortedDescending(t *testing.T) {
	g := New()
	for _, v := range []string{"1.0.0", "0.9.0", "2.0.0-beta", "2.0.0", "1.5.0"} {
		if err := g.AddVersion("p", v); err != nil {
			t.Fatal(err)
		}
	}
	vs := g.Versions("p")
	want := []string{"2.0.0", "2.0.0-beta", "1.5.0", "1.0.0", "0.9.0"}
	if len(vs) != len(want) {
		t.Fatalf("got %d versions, want %d", len(vs), len(want))
	}
	for i, w := range want {
		if vs[i].String() != w {
			t.Errorf("Versions[%d]=%s, want %s", i, vs[i], w)
		}
	}
}
