package tx_test

import (
	"testing"

	"ontology/errs"
	"ontology/spec"
	"ontology/tx"
)

func newStore() *tx.Store {
	s := tx.NewStore()
	s.RegisterType("doc", spec.Schema{Fields: map[string]spec.FieldType{
		"title": spec.FieldString,
		"views": spec.FieldInt,
	}})
	return s
}

// TestValidateRules 覆盖写入参数校验的全部规则：
// 未知类型、目标实例存在性、未知字段、字段类型不符。
func TestValidateRules(t *testing.T) {
	s := newStore()
	tx1 := s.Begin(1)
	tx1.Apply(spec.Write{Type: "doc", ID: "d1", Op: spec.OpCreate,
		Fields: map[string]any{"title": "a", "views": 1}})
	tx1.Commit()

	cases := []struct {
		name  string
		write spec.Write
		valid bool
	}{
		{"create-ok", spec.Write{Type: "doc", ID: "d2", Op: spec.OpCreate,
			Fields: map[string]any{"title": "b"}}, true},
		{"create-existing", spec.Write{Type: "doc", ID: "d1", Op: spec.OpCreate,
			Fields: map[string]any{"title": "b"}}, false},
		{"update-ok", spec.Write{Type: "doc", ID: "d1", Op: spec.OpUpdate,
			Fields: map[string]any{"views": 2}}, true},
		{"update-missing", spec.Write{Type: "doc", ID: "ghost", Op: spec.OpUpdate,
			Fields: map[string]any{"views": 2}}, false},
		{"delete-ok", spec.Write{Type: "doc", ID: "d1", Op: spec.OpDelete}, true},
		{"delete-missing", spec.Write{Type: "doc", ID: "ghost", Op: spec.OpDelete}, false},
		{"unknown-type", spec.Write{Type: "nope", ID: "x", Op: spec.OpCreate}, false},
		{"unknown-field", spec.Write{Type: "doc", ID: "d3", Op: spec.OpCreate,
			Fields: map[string]any{"nope": 1}}, false},
		{"type-mismatch", spec.Write{Type: "doc", ID: "d3", Op: spec.OpCreate,
			Fields: map[string]any{"views": "not-int"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx2 := s.Begin(2)
			defer tx2.Rollback()
			err := tx2.Validate(c.write, []string{"act"})
			t.Logf("输入=%+v 实际错误=%v 期望合法=%v", c.write, err, c.valid)
			if c.valid && err != nil {
				t.Fatalf("应合法，实际 %v", err)
			}
			if !c.valid && !errs.IsKind(err, errs.KindInvalidArgument) {
				t.Fatalf("应为 invalid-argument，实际 %v", err)
			}
		})
	}
}

// TestValidateSeesUncommittedWrites 验证校验基于事务工作副本：
// 同事务内先前已应用的写入对后续校验可见。
func TestValidateSeesUncommittedWrites(t *testing.T) {
	s := newStore()
	tx1 := s.Begin(1)
	create := spec.Write{Type: "doc", ID: "d1", Op: spec.OpCreate,
		Fields: map[string]any{"title": "a"}}
	if err := tx1.Validate(create, nil); err != nil {
		t.Fatalf("create 应合法: %v", err)
	}
	tx1.Apply(create)
	upd := spec.Write{Type: "doc", ID: "d1", Op: spec.OpUpdate,
		Fields: map[string]any{"views": 3}}
	if err := tx1.Validate(upd, nil); err != nil {
		t.Fatalf("同事务内的 update 应合法: %v", err)
	}
	tx1.Rollback()
	tx2 := s.Begin(2)
	defer tx2.Rollback()
	if err := tx2.Validate(upd, nil); !errs.IsKind(err, errs.KindInvalidArgument) {
		t.Fatalf("回退后 update 应为 invalid-argument，实际 %v", err)
	}
}

// TestCommitAndRollbackSemantics 验证提交原子换入、回退不留痕迹。
func TestCommitAndRollbackSemantics(t *testing.T) {
	s := newStore()

	tx1 := s.Begin(1)
	tx1.Apply(spec.Write{Type: "doc", ID: "d1", Op: spec.OpCreate,
		Fields: map[string]any{"title": "committed"}})
	tx1.Commit()
	if _, ok := s.Committed().Get("doc", "d1"); !ok {
		t.Fatal("提交后 d1 应存在")
	}

	tx2 := s.Begin(2)
	tx2.Apply(spec.Write{Type: "doc", ID: "d2", Op: spec.OpCreate,
		Fields: map[string]any{"title": "rolled-back"}})
	tx2.Apply(spec.Write{Type: "doc", ID: "d1", Op: spec.OpDelete})
	tx2.Rollback()
	if _, ok := s.Committed().Get("doc", "d2"); ok {
		t.Fatal("回退后 d2 不应存在")
	}
	fields, ok := s.Committed().Get("doc", "d1")
	if !ok || fields["title"] != "committed" {
		t.Fatalf("回退后 d1 应保持提交时的内容，实际 %v", fields)
	}
}
