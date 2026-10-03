package store

import (
	"errors"
	"testing"

	"ontology/cond"
)

func strp(s string) *string { return &s }
func intp(i int64) *int64   { return &i }

func mustPut(t *testing.T, s *Store, tenant, key string, size int64, etag string, c cond.Cond, now int64) PutResult {
	t.Helper()
	r, err := s.Put(tenant, key, size, etag, c, now)
	if err != nil {
		t.Fatalf("Put(%s,%s,%d,%s) unexpected error: %v", tenant, key, size, etag, err)
	}
	return r
}

func failKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.As(err, new(*cond.FailedConditionError)):
		var pc *cond.FailedConditionError
		errors.As(err, &pc)
		return "precond:" + string(pc.Name)
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrQuotaExceeded):
		return "quota"
	case IsStorageFailure(err):
		return "storage"
	case errors.Is(err, ErrVersioningImmutable):
		return "versioning-immutable"
	default:
		return "other:" + err.Error()
	}
}

// TestTableDriven collects deterministic table-driven checks.
func TestTableDriven(t *testing.T) {
	t.Run("worked_example_unversioned", func(t *testing.T) {
		s := NewStore()
		if err := s.SetQuota("t", 100); err != nil {
			t.Fatal(err)
		}
		ea := mustPut(t, s, "t", "a", 60, "ea", cond.Cond{}, 1).Etag
		if u := s.Used("t"); u != 60 {
			t.Fatalf("U=%d want 60", u)
		}
		if _, err := s.Put("t", "b", 50, "eb", cond.Cond{}, 2); failKind(err) != "quota" {
			t.Fatalf("put b: %v", err)
		}
		mustPut(t, s, "t", "a", 90, "ea2", cond.Cond{IfMatch: strp(ea)}, 3)
		if u := s.Used("t"); u != 90 {
			t.Fatalf("U=%d want 90", u)
		}
		if err := s.SetQuota("t", 80); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put("t", "a", 90, "ea3", cond.Cond{}, 4); err != nil {
			t.Fatalf("d=0 overwrite must pass over quota: %v", err)
		}
		if _, err := s.Put("t", "a", 95, "ea4", cond.Cond{}, 5); failKind(err) != "quota" {
			t.Fatalf("d=5 over quota: %v", err)
		}
		mustPut(t, s, "t", "a", 10, "ea5", cond.Cond{}, 6)
		if u := s.Used("t"); u != 10 {
			t.Fatalf("U=%d want 10", u)
		}
	})

	t.Run("worked_example_versioned", func(t *testing.T) {
		s := NewStore()
		_ = s.SetQuota("t", 100)
		if err := s.SetVersioning("t", true); err != nil {
			t.Fatal(err)
		}
		r1 := mustPut(t, s, "t", "a", 60, "e1", cond.Cond{}, 1)
		if r1.Version != 1 {
			t.Fatalf("first versioned put = v%d want v1", r1.Version)
		}
		if _, err := s.Put("t", "a", 60, "e2", cond.Cond{}, 2); failKind(err) != "quota" {
			t.Fatalf("120>100: %v", err)
		}
		dm, err := s.Delete("t", "a", -1, cond.Cond{}, 3)
		if err != nil || !dm.Marker || dm.Version != 2 {
			t.Fatalf("marker delete = %+v, %v", dm, err)
		}
		dv, err := s.Delete("t", "a", 1, cond.Cond{}, 4)
		if err != nil || dv.Delta != -60 {
			t.Fatalf("delete v1 = %+v, %v", dv, err)
		}
		if u := s.Used("t"); u != 0 {
			t.Fatalf("U=%d want 0", u)
		}
		r3 := mustPut(t, s, "t", "a", 10, "e3", cond.Cond{IfNoneMatchStar: true}, 5)
		if r3.Version != 3 {
			t.Fatalf("IfNoneMatch over marker = v%d want v3", r3.Version)
		}
	})

	t.Run("version0_then_enable", func(t *testing.T) {
		s := NewStore()
		_ = s.SetQuota("t", 100)
		mustPut(t, s, "t", "a", 5, "e0", cond.Cond{}, 1)
		if err := s.SetVersioning("t", true); err != nil {
			t.Fatal(err)
		}
		r := mustPut(t, s, "t", "a", 5, "e1", cond.Cond{}, 2)
		if r.Version != 1 {
			t.Fatalf("version=%d want 1", r.Version)
		}
		if u := s.Used("t"); u != 10 {
			t.Fatalf("U=%d want 10", u)
		}
		if err := s.SetVersioning("t", false); failKind(err) != "versioning-immutable" {
			t.Fatalf("disable: %v", err)
		}
		if err := s.SetVersioning("t", true); err != nil {
			t.Fatalf("re-enable must be idempotent: %v", err)
		}
	})

	t.Run("quota_equality_and_plus_one", func(t *testing.T) {
		s := NewStore()
		_ = s.SetQuota("t", 10)
		mustPut(t, s, "t", "a", 4, "e", cond.Cond{}, 1)
		mustPut(t, s, "t", "a", 10, "e2", cond.Cond{}, 2)
		if u := s.Used("t"); u != 10 {
			t.Fatalf("U=%d want 10 (U+d==Q passes)", u)
		}
		if _, err := s.Put("t", "a", 11, "e3", cond.Cond{}, 3); failKind(err) != "quota" {
			t.Fatalf("Q+1: %v", err)
		}
	})

	t.Run("over_quota_d0_and_d1", func(t *testing.T) {
		s := NewStore()
		_ = s.SetQuota("t", 10)
		mustPut(t, s, "t", "a", 10, "e1", cond.Cond{}, 1)
		_ = s.SetQuota("t", 5)
		mustPut(t, s, "t", "a", 10, "e2", cond.Cond{}, 2)
		if _, err := s.Put("t", "a", 11, "e3", cond.Cond{}, 3); failKind(err) != "quota" {
			t.Fatalf("d=1: %v", err)
		}
	})

	t.Run("precondition_failures_order", func(t *testing.T) {
		s := NewStore()
		_ = s.SetQuota("t", 100)
		mustPut(t, s, "t", "a", 5, "e1", cond.Cond{}, 10)
		if _, err := s.Put("t", "a", 1, "x", cond.Cond{IfMatch: strp("other")}, 11); failKind(err) != "precond:IfMatch" {
			t.Fatalf("IfMatch fail: %v", err)
		}
		if _, err := s.Put("t", "a", 1, "x", cond.Cond{IfUnmodifiedSince: intp(9)}, 11); failKind(err) != "precond:IfUnmodifiedSince" {
			t.Fatalf("IfUnmodifiedSince fail: %v", err)
		}
		mustPut(t, s, "t", "a", 5, "e2", cond.Cond{IfUnmodifiedSince: intp(10)}, 12)
		if _, err := s.Put("t", "a", 1, "x", cond.Cond{IfNoneMatchStar: true}, 13); failKind(err) != "precond:IfNoneMatch" {
			t.Fatalf("IfNoneMatch fail: %v", err)
		}
		all := cond.Cond{IfMatch: strp("other"), IfUnmodifiedSince: intp(0), IfNoneMatchStar: true}
		if _, err := s.Put("t", "a", 1, "x", all, 14); failKind(err) != "precond:IfMatch" {
			t.Fatalf("order: %v", err)
		}
		mix := cond.Cond{IfMatch: strp("e2"), IfUnmodifiedSince: intp(0), IfNoneMatchStar: true}
		if _, err := s.Put("t", "a", 1, "x", mix, 15); failKind(err) != "precond:IfUnmodifiedSince" {
			t.Fatalf("order2: %v", err)
		}
		mix2 := cond.Cond{IfMatch: strp("e2"), IfUnmodifiedSince: intp(12), IfNoneMatchStar: true}
		if _, err := s.Put("t", "a", 1, "x", mix2, 16); failKind(err) != "precond:IfNoneMatch" {
			t.Fatalf("order3: %v", err)
		}
		if _, err := s.Put("t", "a", -1, "x", cond.Cond{IfMatch: strp("nope")}, 17); failKind(err) != "invalid" {
			t.Fatalf("invalid beats precond: %v", err)
		}
		if _, err := s.Put("t", "a", 1, "", cond.Cond{IfUnmodifiedSince: intp(1 << 40)}, 17); failKind(err) != "invalid" {
			t.Fatalf("bad s: %v", err)
		}
	})

	t.Run("marker_current_three_conditions", func(t *testing.T) {
		s := NewStore()
		_ = s.SetQuota("t", 100)
		mustPut(t, s, "t", "a", 5, "e1", cond.Cond{}, 1)
		if err := s.SetVersioning("t", true); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Delete("t", "a", -1, cond.Cond{}, 2); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put("t", "a", 1, "x", cond.Cond{IfMatch: strp("anything")}, 3); failKind(err) != "precond:IfMatch" {
			t.Fatalf("marker IfMatch: %v", err)
		}
		if _, err := s.Put("t", "a", 1, "x", cond.Cond{IfUnmodifiedSince: intp(100)}, 3); failKind(err) != "precond:IfUnmodifiedSince" {
			t.Fatalf("marker IfUnmodifiedSince: %v", err)
		}
		mustPut(t, s, "t", "a", 1, "e2", cond.Cond{IfNoneMatchStar: true}, 3)
	})

	t.Run("marker_on_empty_key_and_delete_version", func(t *testing.T) {
		s := NewStore()
		_ = s.SetQuota("t", 100)
		if err := s.SetVersioning("t", true); err != nil {
			t.Fatal(err)
		}
		dm, err := s.Delete("t", "k", -1, cond.Cond{}, 1)
		if err != nil || !dm.Marker || dm.Version != 1 {
			t.Fatalf("marker on empty: %+v %v", dm, err)
		}
		if _, err := s.Delete("t", "k", 7, cond.Cond{}, 2); failKind(err) != "notfound" {
			t.Fatalf("missing version: %v", err)
		}
		if _, err := s.Delete("t", "k", 1, cond.Cond{}, 3); err != nil {
			t.Fatalf("remove marker: %v", err)
		}
		if v := s.Current("t", "k"); v.Exists {
			t.Fatalf("current must not exist after marker removed")
		}
	})
}
