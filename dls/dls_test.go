package dls

import (
	"errors"
	"ontology/role"
	"testing"
)

func ptr(e role.Expr) *role.Expr { return &e }

func term(t *testing.T, f string, v role.Value) *role.Expr {
	t.Helper()
	e, err := role.Term(f, v)
	if err != nil {
		t.Fatal(err)
	}
	return &e
}

func rng(t *testing.T, f string, lo, hi int64) *role.Expr {
	t.Helper()
	e, err := role.Range(f, lo, hi)
	if err != nil {
		t.Fatal(err)
	}
	return &e
}

func setup(t *testing.T, binds map[string][]string, roles map[string][]role.Entry) (*Resolver, *role.Registry) {
	t.Helper()
	reg := role.NewRegistry()
	for name, entries := range roles {
		if err := reg.PutRole(name, entries); err != nil {
			t.Fatal(err)
		}
	}
	for user, rs := range binds {
		if err := reg.BindUser(user, rs); err != nil {
			t.Fatal(err)
		}
	}
	return NewResolver(reg), reg
}

func TestMergeTableDriven(t *testing.T) {
	roles := map[string][]role.Entry{
		"R1": {{Pattern: "logs-*", Filter: term(t, "team", "a"),
			Fields: role.FieldAuth{Grant: []string{"team", "level"}}}},
		"R2": {{Pattern: "logs-1", Filter: rng(t, "level", 5, 9),
			Fields: role.FieldAuth{Grant: []string{"*"}, Except: []string{"secret"}}}},
		"R3": {{Pattern: "logs-1", Filter: nil,
			Fields: role.FieldAuth{Grant: []string{"secret"}}}},
	}
	binds := map[string][]string{
		"u1":  {"R1"},
		"u12": {"R1", "R2"},
		"u13": {"R1", "R3"},
	}
	rv, reg := setup(t, binds, roles)
	_ = reg

	docs := []map[string]role.Value{
		{"id-x": "x", "team": "a", "level": int64(3), "secret": "k1"},
		{"id-x": "x", "team": "b", "level": int64(5), "secret": "k2"},
		{"id-x": "x", "team": "a", "level": int64(9)},
	}
	cases := []struct {
		user           string
		docsOpen       bool
		wantVisible    []bool
		fieldVisible   map[string]bool
		matchedEntries int
	}{
		{
			user: "u1", docsOpen: false, wantVisible: []bool{true, false, true},
			fieldVisible:   map[string]bool{"team": true, "level": true, "secret": false},
			matchedEntries: 1,
		},
		{
			user: "u12", docsOpen: false, wantVisible: []bool{true, true, true},
			fieldVisible:   map[string]bool{"team": true, "level": true, "secret": false},
			matchedEntries: 2,
		},
		{
			user: "u13", docsOpen: true, wantVisible: []bool{true, true, true},
			fieldVisible:   map[string]bool{"team": true, "level": true, "secret": true, "other": false},
			matchedEntries: 2,
		},
	}
	for _, c := range cases {
		v, err := rv.Resolve(c.user, "logs-1")
		if err != nil {
			t.Fatalf("%s: %v", c.user, err)
		}
		if v.DocsOpen != c.docsOpen {
			t.Errorf("%s: DocsOpen=%v want %v", c.user, v.DocsOpen, c.docsOpen)
		}
		if v.MatchedEntries() != c.matchedEntries {
			t.Errorf("%s: matched=%d want %d", c.user, v.MatchedEntries(), c.matchedEntries)
		}
		for i, d := range docs {
			if got := v.DocVisible(d); got != c.wantVisible[i] {
				t.Errorf("%s: doc[%d] visible=%v want %v", c.user, i, got, c.wantVisible[i])
			}
		}
		for f, want := range c.fieldVisible {
			if got := v.FieldVisible(f); got != want {
				t.Errorf("%s: field %q visible=%v want %v", c.user, f, got, want)
			}
		}
	}

	// 前缀通配边界：logs-* 不匹配 logs9，但匹配 logs-。
	v, err := rv.Resolve("u1", "logs9")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("logs9 err=%v want forbidden", err)
	}
	v, err = rv.Resolve("u1", "logs-")
	if err != nil || !v.DocVisible(map[string]role.Value{"team": "a"}) {
		t.Fatalf("logs- should match logs-*: v=%v err=%v", v, err)
	}
}

func TestResolveErrors(t *testing.T) {
	reg := role.NewRegistry()
	if err := reg.PutRole("R", []role.Entry{{Pattern: "logs-*", Fields: role.FieldAuth{Grant: []string{"*"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.BindUser("u", []string{"R"}); err != nil {
		t.Fatal(err)
	}
	rv := NewResolver(reg)

	if _, err := rv.Resolve("u", ""); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad index err=%v", err)
	}
	if _, err := rv.Resolve("", "i"); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad user err=%v", err)
	}
	if _, err := rv.Resolve("ghost", "i"); !errors.Is(err, role.ErrUserNotFound) {
		t.Fatalf("missing user err=%v", err)
	}
	// 无权：无匹配条目。
	if _, err := rv.Resolve("u", "other"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("no match err=%v", err)
	}
	// 参数非法先于用户不存在。
	if _, err := rv.Resolve("", ""); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("invalid must precede user-not-found: %v", err)
	}
}

func TestTouchedIndependentOfOtherRoles(t *testing.T) {
	for _, n := range []int{10, 10000} {
		reg := role.NewRegistry()
		entries := []role.Entry{{Pattern: "logs-*", Fields: role.FieldAuth{Grant: []string{"*"}}}}
		for i := 0; i < 3; i++ {
			entries = append(entries, role.Entry{Pattern: "x-*", Fields: role.FieldAuth{Grant: []string{"f"}}})
		}
		if err := reg.PutRole("Rmine", entries); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			name := "R" + string(rune('a'+i%26)) + itoa(i)
			if err := reg.PutRole(name, []role.Entry{{Pattern: "*", Fields: role.FieldAuth{Unrestricted: true}}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := reg.BindUser("u", []string{"Rmine"}); err != nil {
			t.Fatal(err)
		}
		rv := NewResolver(reg)
		v, err := rv.Resolve("u", "logs-1")
		if err != nil {
			t.Fatal(err)
		}
		if v.touched != 4 {
			t.Fatalf("other roles=%d: touched=%d want 4 (user's own entries)", n, v.touched)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
