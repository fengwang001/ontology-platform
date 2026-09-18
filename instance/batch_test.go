package instance

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBatchWriteSuccess(t *testing.T) {
	s := NewStore(personSpec())

	if _, err := s.Create("person", "u", map[string]any{"name": "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("person", "u", 1, map[string]any{"name": "old2"}); err != nil {
		t.Fatal(err) // u at v2
	}
	if _, err := s.Create("person", "d", map[string]any{"name": "doomed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("person", "gone", map[string]any{"name": "ghost"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("person", "gone", 1); err != nil {
		t.Fatal(err)
	}

	err := s.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "person", Key: "c", Properties: map[string]any{"name": "new"}},
		{Kind: OpUpdate, ObjectType: "person", Key: "u", ExpectedVersion: 2, Properties: map[string]any{"name": "patched"}},
		{Kind: OpDelete, ObjectType: "person", Key: "d", ExpectedVersion: 1},
		// Resurrection inside a batch continues version history.
		{Kind: OpCreate, ObjectType: "person", Key: "gone", Properties: map[string]any{"name": "back"}},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}

	c, _ := s.Get("person", "c")
	if c.Version != 1 || c.Properties["name"] != "new" {
		t.Fatalf("created: %+v", c)
	}
	u, _ := s.Get("person", "u")
	if u.Version != 3 || u.Properties["name"] != "patched" {
		t.Fatalf("updated: %+v", u)
	}
	if _, err := s.Get("person", "d"); !errors.Is(err, ErrDeleted) {
		t.Fatalf("deleted: %v", err)
	}
	g, _ := s.Get("person", "gone")
	if g.Version != 2 || g.Properties["name"] != "back" {
		t.Fatalf("resurrected in batch: %+v", g)
	}
	if !c.LastWriteTime.Equal(u.LastWriteTime) || !c.LastWriteTime.Equal(g.LastWriteTime) {
		t.Fatal("batch writes did not share a timestamp")
	}
}

func TestBatchWriteDuplicateKey(t *testing.T) {
	s := NewStore(personSpec())

	err := s.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "person", Key: "same", Properties: map[string]any{"name": "a"}},
		{Kind: OpCreate, ObjectType: "person", Key: "other", Properties: map[string]any{"name": "b"}},
		{Kind: OpUpdate, ObjectType: "person", Key: "same", ExpectedVersion: 1, Properties: map[string]any{"name": "c"}},
	})
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BatchError", err)
	}
	if be.Kind != KindDuplicateKey || be.Index != 2 || be.Key != "same" {
		t.Fatalf("batch error = %+v, want duplicate-key at index 2 key same", be)
	}

	// Nothing was applied, not even earlier ops in the batch.
	if _, err := s.Get("person", "same"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first op leaked after rejection: %v", err)
	}
	if _, err := s.Get("person", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second op leaked after rejection: %v", err)
	}

	// Adjacent create + delete of a fresh key is also a duplicate,
	// never silently merged.
	err = s.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "person", Key: "x", Properties: map[string]any{"name": "x"}},
		{Kind: OpDelete, ObjectType: "person", Key: "x", ExpectedVersion: 1},
	})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("create+delete same key err = %v, want duplicate", err)
	}
}

func TestBatchWriteRollback(t *testing.T) {
	s := NewStore(personSpec())

	if _, err := s.Create("person", "keep", map[string]any{"name": "before"}); err != nil {
		t.Fatal(err)
}
	if _, err := s.Create("person", "del", map[string]any{"name": "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("person", "tomb", map[string]any{"name": "t"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("person", "tomb", 1); err != nil {
		t.Fatal(err)
	}

	keepBefore, _ := s.Get("person", "keep")

	ops := []Op{
		// 0: valid create (must be rolled back)
		{Kind: OpCreate, ObjectType: "person", Key: "new1", Properties: map[string]any{"name": "n"}},
		// 1: valid update (must be rolled back)
		{Kind: OpUpdate, ObjectType: "person", Key: "keep", ExpectedVersion: 1, Properties: map[string]any{"name": "changed"}},
		// 2: valid delete (must be rolled back)
		{Kind: OpDelete, ObjectType: "person", Key: "del", ExpectedVersion: 1},
		// 3: valid resurrection (must be rolled back)
		{Kind: OpCreate, ObjectType: "person", Key: "tomb", Properties: map[string]any{"name": "revived"}},
		// 4: stale delete -> conflict, actual v1
		{Kind: OpDelete, ObjectType: "person", Key: "keep", ExpectedVersion: 7},
	}

	err := s.BatchWrite(ops)
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want *BatchError", err)
	}
	if be.Index != 4 || be.Kind != KindConflict || be.Key != "keep" ||
		be.Expected != 7 || be.Actual != 1 {
		t.Fatalf("batch error = %+v, want conflict at index 4 expected 7 actual 1", be)
	}

	// Full rollback: no version advanced, no timestamp changed.
	keepAfter, err := s.Get("person", "keep")
	if err != nil {
		t.Fatalf("keep vanished: %v", err)
	}
	if keepAfter.Version != 1 || keepAfter.Properties["name"] != "before" {
		t.Fatalf("update was not rolled back: %+v", keepAfter)
	}
	if !keepAfter.LastWriteTime.Equal(keepBefore.LastWriteTime) {
		t.Fatal("LastWriteTime changed despite rollback")
	}
	if _, err := s.Get("person", "new1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create leaked after rollback: %v", err)
	}
	if _, err := s.Get("person", "del"); err != nil {
		t.Fatalf("delete leaked after rollback: %v", err)
	}
	if _, err := s.Get("person", "tomb"); !errors.Is(err, ErrDeleted) {
		t.Fatalf("resurrection leaked after rollback: %v", err)
	}

	// Constraint failure mid-batch also rolls back, reporting index/key/kind.
	err = s.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "person", Key: "new2", Properties: map[string]any{"name": "n"}},
		{Kind: OpCreate, ObjectType: "person", Key: "bad", Properties: map[string]any{"age": 1}}, // missing name
	})
	if !errors.As(err, &be) || be.Index != 1 || be.Kind != KindConstraint || be.Key != "bad" {
		t.Fatalf("constraint batch err = %+v", be)
	}
	if _, err := s.Get("person", "new2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("op 0 leaked after constraint rollback: %v", err)
	}

	// Deleted/not-found categories survive the batch wrapper via
	// errors.As into *Error.
	err = s.BatchWrite([]Op{
		{Kind: OpUpdate, ObjectType: "person", Key: "tomb", ExpectedVersion: 1, Properties: map[string]any{"name": "x"}},
	})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindDeleted {
		t.Fatalf("batch update deleted err = %v, want KindDeleted", err)
	}

	err = s.BatchWrite([]Op{
		{Kind: OpDelete, ObjectType: "person", Key: "ghost", ExpectedVersion: 1},
	})
	if !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Fatalf("batch delete unknown err = %v, want KindNotFound", err)
	}
}

func TestBatchWriteEmpty(t *testing.T) {
	s := NewStore()
	if err := s.BatchWrite(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
}
