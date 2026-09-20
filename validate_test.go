package ontology

import (
	"errors"
	"testing"
)

// 三类校验失败：未知字段、类型不匹配、整数越界。
// 每种失败都必须可判定类别、指明字段，且版本号不前进。
func TestUpdateValidationFailures(t *testing.T) {
	cases := []struct {
		name    string
		changes map[string]any
		wantErr error
		field   string
	}{
		{"unknown field", map[string]any{"ghost": "x"}, ErrUnknownField, "ghost"},
		{"type mismatch", map[string]any{"port": "not-an-int"}, ErrTypeMismatch, "port"},
		{"out of range", map[string]any{"port": int64(70000)}, ErrOutOfRange, "port"},
		{"out of range low", map[string]any{"retries": int64(-1)}, ErrOutOfRange, "retries"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestManager(t)
			before := m.Current()
			_, err := m.Update(c.changes)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Field != c.field {
				t.Fatalf("ve.Field = %q, want %q", ve.Field, c.field)
			}
			if got := m.Current(); got != before {
				t.Fatalf("version advanced to %d on failed update", got)
			}
		})
	}
}

// 原子性：一次更新含多个字段，任何一项不通过则整次不生效。
func TestUpdateAtomicity(t *testing.T) {
	m := newTestManager(t)
	_, err := m.Update(map[string]any{
		"host": "b",             // 合法
		"port": int64(99999999), // 越界
	})
	if !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("err = %v, want ErrOutOfRange", err)
	}
	if got := m.Current(); got != 1 {
		t.Fatalf("version = %d, want 1 (no partial update)", got)
	}
	host, err := m.GetAt(1, "host")
	if err != nil {
		t.Fatalf("GetAt(1, host): %v", err)
	}
	if host != "a" {
		t.Fatalf("host = %v, want a (legal field must not be applied)", host)
	}
}

// NewManager 的初始内容同样整体校验。
func TestNewManagerValidation(t *testing.T) {
	schema, err := NewSchema(Field{Name: "port", Kind: IntKind, Min: 1, Max: 10})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	if _, err := NewManager(schema, map[string]any{"port": int64(99)}); err == nil {
		t.Fatal("NewManager with out-of-range initial: want error, got nil")
	}
	if _, err := NewManager(schema, map[string]any{}); err == nil {
		t.Fatal("NewManager with missing field: want error, got nil")
	}
	if _, err := NewManager(schema, map[string]any{
		"port": int64(5), "ghost": "x",
	}); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("NewManager with unknown field err = %v, want ErrUnknownField", err)
	}
}

// Schema 自身的非法声明要被拒绝。
func TestSchemaDeclaration(t *testing.T) {
	if _, err := NewSchema(Field{Name: "", Kind: StringKind}); err == nil {
		t.Fatal("empty field name: want error, got nil")
	}
	if _, err := NewSchema(
		Field{Name: "a", Kind: StringKind},
		Field{Name: "a", Kind: StringKind},
	); err == nil {
		t.Fatal("duplicate field: want error, got nil")
	}
	if _, err := NewSchema(
		Field{Name: "n", Kind: IntKind, Min: 10, Max: 1},
	); err == nil {
		t.Fatal("min > max: want error, got nil")
	}
}
