package ontology

import (
	"errors"
	"testing"
)

func newTestStore() *Store { return NewStore(nil) }

func TestCreateGetUpdateVersioning(t *testing.T) {
	s := newTestStore()
	inst, err := s.Create("Robot", "r1", map[string]any{"name": "walle"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if inst.Version != 1 {
		t.Fatalf("first version = %d, want 1", inst.Version)
	}
	if inst.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt not set")
	}

	got, err := s.Get("Robot", "r1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 || got.Properties["name"] != "walle" {
		t.Fatalf("unexpected get result: %+v", got)
	}

	updated, err := s.Update("Robot", "r1", 1, map[string]any{"name": "eve"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version after update = %d, want 2", updated.Version)
	}
	if !updated.UpdatedAt.After(inst.UpdatedAt) && !updated.UpdatedAt.Equal(inst.UpdatedAt) {
		t.Fatal("UpdatedAt moved backwards")
	}
}

func TestUpdateVersionConflict(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "r1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("Robot", "r1", 1, nil); err != nil {
		t.Fatal(err)
	}

	_, err := s.Update("Robot", "r1", 1, nil)
	var vc *VersionConflictError
	if !errors.As(err, &vc) {
		t.Fatalf("err = %v, want VersionConflictError", err)
	}
	if vc.Expected != 1 || vc.Actual != 2 {
		t.Fatalf("conflict expected=%d actual=%d, want 1/2", vc.Expected, vc.Actual)
	}
}

func TestDeleteLogicalAndErrorCategories(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "r1", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("Robot", "r1", 1); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Get 不可见
	var nf *NotFoundError
	if _, err := s.Get("Robot", "r1"); !errors.As(err, &nf) {
		t.Fatalf("get deleted: err = %v, want NotFoundError", err)
	}

	// 更新已删除实例：DeletedError，且与 NotFoundError 不同类别
	_, err := s.Update("Robot", "r1", 2, nil)
	var del *DeletedError
	if !errors.As(err, &del) {
		t.Fatalf("update deleted: err = %v, want DeletedError", err)
	}
	if errors.As(err, &nf) {
		t.Fatal("DeletedError must not degrade to NotFoundError")
	}
	var vc *VersionConflictError
	if errors.As(err, &vc) {
		t.Fatal("DeletedError must not degrade to VersionConflictError")
	}

	// 从未存在的主键：NotFoundError，且与 DeletedError 不同类别
	_, err = s.Update("Robot", "ghost", 1, nil)
	if !errors.As(err, &nf) {
		t.Fatalf("update missing: err = %v, want NotFoundError", err)
	}
	if errors.As(err, &del) {
		t.Fatal("NotFoundError must not be a DeletedError")
	}

	// 删除已删除实例：DeletedError，不是版本冲突
	if err := s.Delete("Robot", "r1", 2); !errors.As(err, &del) {
		t.Fatalf("re-delete: err = %v, want DeletedError", err)
	}
}

func TestReviveContinuesVersion(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "r1", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("Robot", "r1", 1, map[string]any{"n": 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("Robot", "r1", 2); err != nil {
		t.Fatal(err)
	}

	revived, err := s.Create("Robot", "r1", map[string]any{"n": 3})
	if err != nil {
		t.Fatalf("revive: %v", err)
	}
	if revived.Version <= 2 {
		t.Fatalf("revived version = %d, must continue past pre-delete version 2", revived.Version)
	}
	if revived.Version == 1 {
		t.Fatal("revived version must never reset to 1")
	}
	if revived.Properties["n"] != 3 {
		t.Fatalf("revived props = %v", revived.Properties)
	}

	// 复活后可正常按新版本更新
	if _, err := s.Update("Robot", "r1", revived.Version, nil); err != nil {
		t.Fatalf("update after revive: %v", err)
	}
}

func TestCreateDuplicateAlive(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "r1", nil); err != nil {
		t.Fatal(err)
	}
	var ae *AlreadyExistsError
	if _, err := s.Create("Robot", "r1", nil); !errors.As(err, &ae) {
		t.Fatalf("err = %v, want AlreadyExistsError", err)
	}
}
