package query

import (
	"errors"
	"testing"

	"ontology/dls"
	"ontology/role"
)

// 表达式助手：失败即 t.Fatal。
func eTerm(t *testing.T, f string, v role.Value) role.Expr {
	t.Helper()
	e, err := role.Term(f, v)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func eRange(t *testing.T, f string, lo, hi int64) role.Expr {
	t.Helper()
	e, err := role.Range(f, lo, hi)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func eNot(t *testing.T, c role.Expr) role.Expr {
	t.Helper()
	e, err := role.Not(c)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func ePtr(e role.Expr) *role.Expr { return &e }

func mustBind(t *testing.T, s *Store, user string, roles []string) {
	t.Helper()
	if err := s.Registry().BindUser(user, roles); err != nil {
		t.Fatal(err)
	}
}

func newSpecStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	s.PutDoc("logs-1", "d1", map[string]role.Value{"team": "a", "level": int64(3), "secret": "k1"})
	s.PutDoc("logs-1", "d2", map[string]role.Value{"team": "b", "level": int64(5), "secret": "k2"})
	s.PutDoc("logs-1", "d3", map[string]role.Value{"team": "a", "level": int64(9)})
	reg := s.Registry()
	r1 := role.Entry{Pattern: "logs-*", Filter: ePtr(eTerm(t, "team", "a")),
		Fields: role.FieldAuth{Grant: []string{"team", "level"}}}
	r2 := role.Entry{Pattern: "logs-1", Filter: ePtr(eRange(t, "level", 5, 9)),
		Fields: role.FieldAuth{Grant: []string{"*"}, Except: []string{"secret"}}}
	r3 := role.Entry{Pattern: "logs-1", Filter: nil,
		Fields: role.FieldAuth{Grant: []string{"secret"}}}
	for name, entries := range map[string][]role.Entry{"R1": {r1}, "R2": {r2}, "R3": {r3}} {
		if err := reg.PutRole(name, entries); err != nil {
			t.Fatal(err)
		}
	}
	mustBind(t, s, "u1", []string{"R1"})
	mustBind(t, s, "u12", []string{"R1", "R2"})
	mustBind(t, s, "u13", []string{"R1", "R3"})
	return s
}

func TestSpecExamples(t *testing.T) {
	s := newSpecStore(t)

	// 只绑 R1：d1、d3 可见，字段 team、level。
	got, total, err := s.Search("u1", "logs-1", eTerm(t, "team", "a"), 10)
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("u1 search: got=%d total=%d err=%v", len(got), total, err)
	}
	if got[0].ID != "d1" || got[1].ID != "d3" {
		t.Fatalf("u1 ids=%s,%s", got[0].ID, got[1].ID)
	}
	if _, leak := got[0].Fields["secret"]; leak {
		t.Fatal("secret must be pruned for u1")
	}
	if _, err := s.Get("u1", "logs-1", "d2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u1 Get d2 err=%v want not found", err)
	}
	agg, err := s.Agg("u1", "logs-1", "level")
	if err != nil || len(agg) != 2 || agg[0] != (AggItem{int64(3), 1}) ||
		agg[1] != (AggItem{int64(9), 1}) {
		t.Fatalf("u1 Agg level=%+v err=%v", agg, err)
	}

	// u12：三篇都可见；secret 不可见；Not(Term(secret,"k1")) 恒真。
	notSecret := eNot(t, eTerm(t, "secret", "k1"))
	got, total, err = s.Search("u12", "logs-1", notSecret, 10)
	if err != nil || total != 3 || len(got) != 3 {
		t.Fatalf("u12 not-secret: total=%d n=%d err=%v", total, len(got), err)
	}
	for _, d := range got {
		if _, leak := d.Fields["secret"]; leak {
			t.Fatalf("secret leaked in %s", d.ID)
		}
	}
	aggSecret, err := s.Agg("u12", "logs-1", "secret")
	if err != nil || len(aggSecret) != 0 {
		t.Fatalf("u12 Agg secret must be empty: %+v %v", aggSecret, err)
	}

	// u13：空 filter → 三篇都可见，字段并集含 secret。
	got, total, err = s.Search("u13", "logs-1", eTerm(t, "secret", "k2"), 10)
	if err != nil || total != 1 || len(got) != 1 || got[0].ID != "d2" {
		t.Fatalf("u13 secret search: total=%d n=%d err=%v", total, len(got), err)
	}
	if got[0].Fields["secret"] != "k2" || got[0].Fields["level"] != int64(5) {
		t.Fatalf("u13 fields=%v", got[0].Fields)
	}

	// 索引不存在 与 无权 不可区分。
	for _, idx := range []string{"logs-9", "other"} {
		if _, _, err := s.Search("u12", idx, notSecret, 10); !errors.Is(err, dls.ErrForbidden) {
			t.Fatalf("Search %s err=%v want forbidden", idx, err)
		}
		if _, err := s.Get("u12", idx, "d1"); !errors.Is(err, dls.ErrForbidden) {
			t.Fatalf("Get %s err=%v want forbidden", idx, err)
		}
		if _, err := s.Agg("u12", idx, "level"); !errors.Is(err, dls.ErrForbidden) {
			t.Fatalf("Agg %s err=%v want forbidden", idx, err)
		}
	}
}

func TestSearchSizeAndOrder(t *testing.T) {
	s := newSpecStore(t)
	all := eNot(t, eTerm(t, "secret", "k1"))
	got, total, err := s.Search("u12", "logs-1", all, 2)
	if err != nil || total != 3 || len(got) != 2 || got[0].ID != "d1" || got[1].ID != "d2" {
		t.Fatalf("size 2: %+v total=%d err=%v", got, total, err)
	}
	for _, n := range []int{0, 1001} {
		if _, _, err := s.Search("u12", "logs-1", all, n); !errors.Is(err, role.ErrInvalid) {
			t.Fatalf("size %d err=%v", n, err)
		}
	}
}

func TestErrorOrder(t *testing.T) {
	s := newSpecStore(t)
	q := eTerm(t, "f", "x")
	if _, _, err := s.Search("", "logs-1", q, 10); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad user err=%v", err)
	}
	if _, _, err := s.Search("ghost", "logs-1", q, 10); !errors.Is(err, role.ErrUserNotFound) {
		t.Fatalf("ghost user err=%v", err)
	}
	if _, err := s.Get("u12", "logs-1", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing doc err=%v", err)
	}
}

func TestAdminOps(t *testing.T) {
	s := newSpecStore(t)
	s.DeleteDoc("logs-1", "d2")
	if _, err := s.Get("u13", "logs-1", "d2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted doc err=%v", err)
	}
	s.DeleteDoc("logs-1", "d2") // 幂等
	s.PutDoc("new-1", "x", map[string]role.Value{"f": int64(1)})
	if err := s.Registry().PutRole("RN", []role.Entry{{Pattern: "new-*",
		Fields: role.FieldAuth{Grant: []string{"*"}}}}); err != nil {
		t.Fatal(err)
	}
	mustBind(t, s, "un", []string{"RN"})
	d, err := s.Get("un", "new-1", "x")
	if err != nil || d.Fields["f"] != int64(1) {
		t.Fatalf("new index Get=%+v err=%v", d, err)
	}
}
