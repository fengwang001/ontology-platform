package ontology

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBatchWriteMixedSuccess(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "keep", map[string]any{"v": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Robot", "drop", nil); err != nil {
		t.Fatal(err)
	}

	err := s.BatchWrite([]WriteOp{
		{Kind: OpCreate, ObjectType: "Robot", PrimaryKey: "new", Properties: map[string]any{"v": 9}},
		{Kind: OpUpdate, ObjectType: "Robot", PrimaryKey: "keep", ExpectedVersion: 1, Properties: map[string]any{"v": 2}},
		{Kind: OpDelete, ObjectType: "Robot", PrimaryKey: "drop", ExpectedVersion: 1},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}

	if got, _ := s.Get("Robot", "new"); got == nil || got.Version != 1 {
		t.Fatalf("created = %+v", got)
	}
	if got, _ := s.Get("Robot", "keep"); got == nil || got.Version != 2 || got.Properties["v"] != 2 {
		t.Fatalf("updated = %+v", got)
	}
	var nf *NotFoundError
	if _, err := s.Get("Robot", "drop"); !errors.As(err, &nf) {
		t.Fatalf("deleted still visible: %v", err)
	}
}

// 批内一条版本冲突：整批回滚，版本不前进，时间不变化。
func TestBatchWriteRollbackOnConflict(t *testing.T) {
	s := newTestStore()
	a, err := s.Create("Robot", "a", map[string]any{"v": 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Robot", "b", nil); err != nil {
		t.Fatal(err)
	}

	err = s.BatchWrite([]WriteOp{
		{Kind: OpUpdate, ObjectType: "Robot", PrimaryKey: "a", ExpectedVersion: 1, Properties: map[string]any{"v": 99}},
		{Kind: OpUpdate, ObjectType: "Robot", PrimaryKey: "b", ExpectedVersion: 7}, // 冲突
		{Kind: OpCreate, ObjectType: "Robot", PrimaryKey: "c"},
	})

	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BatchError", err)
	}
	if be.Index != 1 || be.PrimaryKey != "b" {
		t.Fatalf("batch error points at op %d (%s), want op 1 (b)", be.Index, be.PrimaryKey)
	}
	var vc *VersionConflictError
	if !errors.As(err, &vc) || vc.Expected != 7 || vc.Actual != 1 {
		t.Fatalf("cause = %v, want VersionConflictError 7 vs 1", err)
	}

	// 完全回滚：a 未变、c 未创建、版本与写入时间均未前进。
	got, err := s.Get("Robot", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Properties["v"] != 1 {
		t.Fatalf("op 0 leaked: %+v", got)
	}
	if !got.UpdatedAt.Equal(a.UpdatedAt) {
		t.Fatal("UpdatedAt advanced despite rollback")
	}
	var nf *NotFoundError
	if _, err := s.Get("Robot", "c"); !errors.As(err, &nf) {
		t.Fatalf("op 2 leaked: %v", err)
	}
}

func TestBatchWriteDuplicateKeyRejected(t *testing.T) {
	s := newTestStore()
	err := s.BatchWrite([]WriteOp{
		{Kind: OpCreate, ObjectType: "Robot", PrimaryKey: "x"},
		{Kind: OpDelete, ObjectType: "Robot", PrimaryKey: "x", ExpectedVersion: 1},
	})
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BatchError", be)
	}
	if be.Index != 1 || be.PrimaryKey != "x" {
		t.Fatalf("duplicate reported at op %d (%s), want op 1 (x)", be.Index, be.PrimaryKey)
	}
	if _, getErr := s.Get("Robot", "x"); getErr == nil {
		t.Fatal("duplicate batch partially applied")
	}
}

func TestBatchWriteConstraintViolationRollsBack(t *testing.T) {
	s := NewStore(func(_ string, props map[string]any) error {
		if _, ok := props["name"]; !ok {
			return fmt.Errorf("name is required")
		}
		return nil
	})
	if _, err := s.Create("Robot", "ok", map[string]any{"name": "a"}); err != nil {
		t.Fatal(err)
	}

	err := s.BatchWrite([]WriteOp{
		{Kind: OpUpdate, ObjectType: "Robot", PrimaryKey: "ok", ExpectedVersion: 1, Properties: map[string]any{"name": "b"}},
		{Kind: OpCreate, ObjectType: "Robot", PrimaryKey: "bad", Properties: map[string]any{}},
	})
	var ce *ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want ConstraintError", err)
	}
	if !strings.Contains(ce.Reason, "name is required") {
		t.Fatalf("reason = %q", ce.Reason)
	}
	got, _ := s.Get("Robot", "ok")
	if got.Version != 1 || got.Properties["name"] != "a" {
		t.Fatalf("valid op leaked despite sibling violation: %+v", got)
	}
}

func TestBatchWriteDeletedAndMissingCauses(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create("Robot", "gone", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("Robot", "gone", 1); err != nil {
		t.Fatal(err)
	}

	err := s.BatchWrite([]WriteOp{
		{Kind: OpUpdate, ObjectType: "Robot", PrimaryKey: "gone", ExpectedVersion: 2},
	})
	var del *DeletedError
	if !errors.As(err, &del) {
		t.Fatalf("err = %v, want DeletedError", err)
	}

	err = s.BatchWrite([]WriteOp{
		{Kind: OpDelete, ObjectType: "Robot", PrimaryKey: "never", ExpectedVersion: 1},
	})
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}
