package cdc

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// logProjection 按测试要求打印事件、目标模式与逐列判定记录或错误。
func logProjection(t *testing.T, e Event, schema Schema, p Projection, err error) {
	t.Helper()
	t.Logf("event(version=%d) fields=%s", e.Version, formatFields(e.Fields))
	t.Logf("target schema version=%d columns=%s", schema.Version, formatColumns(schema.Columns))
	for _, tr := range p.Trace {
		t.Logf("  column %q: action=%s in=%v out=%v reason=%s", tr.Column, tr.Action, tr.In, tr.Out, tr.Reason)
	}
	if err != nil {
		t.Logf("projection REJECTED: %v", err)
	} else {
		t.Logf("projection values=%s", formatFields(p.Values))
	}
}

func formatFields(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s:%v", k, m[k])
	}
	sb.WriteString("}")
	return sb.String()
}

func formatColumns(cols []Column) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		req := "optional"
		if c.Required {
			req = "required"
		}
		parts = append(parts, fmt.Sprintf("%s:%s(%s)", c.Name, c.Type, req))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// manualProject 是独立于实现的手工逐列映射，用于对照核对 Project 的结果。
// 规则与文档一致：按列名对齐；缺失必填拒收、缺失可选补零值；
// int->string 总成功；string->int 仅合法整数成功；多余字段丢弃。
func manualProject(cols []Column, fields map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(cols))
	for _, c := range cols {
		raw, ok := fields[c.Name]
		if !ok {
			if c.Required {
				return nil, fmt.Errorf("manual: required column %q missing", c.Name)
			}
			if c.Type == TypeInt {
				out[c.Name] = int64(0)
			} else {
				out[c.Name] = ""
			}
			continue
		}
		switch c.Type {
		case TypeInt:
			switch v := raw.(type) {
			case int:
				out[c.Name] = int64(v)
			case int64:
				out[c.Name] = v
			case string:
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("manual: column %q not an integer: %q", c.Name, v)
				}
				out[c.Name] = n
			default:
				return nil, fmt.Errorf("manual: column %q bad value type %T", c.Name, raw)
			}
		case TypeString:
			switch v := raw.(type) {
			case string:
				out[c.Name] = v
			case int:
				out[c.Name] = strconv.Itoa(v)
			case int64:
				out[c.Name] = strconv.FormatInt(v, 10)
			default:
				return nil, fmt.Errorf("manual: column %q bad value type %T", c.Name, raw)
			}
		}
	}
	return out, nil
}

// checkAgainstManual 用逐列手工映射对照核对一次投影。
func checkAgainstManual(t *testing.T, r *Registry, e Event, target int) (Projection, error) {
	t.Helper()
	schema, ok := r.Get(target)
	if !ok {
		t.Fatalf("target version %d not registered", target)
	}
	p, err := r.ProjectWithTrace(e, target)
	logProjection(t, e, schema, p, err)

	want, wantErr := manualProject(schema.Columns, e.Fields)
	if wantErr != nil {
		if err == nil {
			t.Fatalf("manual mapping rejects (%v) but Project succeeded: %v", wantErr, p.Values)
		}
		return p, err
	}
	if err != nil {
		t.Fatalf("manual mapping accepts (%v) but Project rejected: %v", want, err)
	}
	if !reflect.DeepEqual(p.Values, want) {
		t.Fatalf("mismatch with manual mapping:\n got=%v\nwant=%v", p.Values, want)
	}
	return p, nil
}

// baseSchema 返回一组基础列：id 必填 int，name 可选 string。
func baseSchema() []Column {
	return []Column{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "name", Type: TypeString},
	}
}

// TestProjectNameAlignment 列按名字而非位置对齐：
// 事件字段顺序与模式列序不同，结果仍按列名正确对应。
func TestProjectNameAlignment(t *testing.T) {
	r := NewRegistry()
	v, err := r.Register([]Column{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "name", Type: TypeString},
		{Name: "age", Type: TypeInt},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// 字段故意以与模式相反的顺序给出（map 本身无序，此处强调不依赖位置）。
	e := Event{Version: v, Fields: map[string]any{
		"age":  int64(30),
		"name": "alice",
		"id":   int64(7),
	}}
	p, err := checkAgainstManual(t, r, e, v)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if p.Values["id"] != int64(7) || p.Values["name"] != "alice" || p.Values["age"] != int64(30) {
		t.Fatalf("columns not aligned by name: %v", p.Values)
	}
}

// TestProjectMissingOptionalZeroFill 缺失可选列时补该类型零值。
func TestProjectMissingOptionalZeroFill(t *testing.T) {
	r := NewRegistry()
	v, err := r.Register(baseSchema())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	e := Event{Version: v, Fields: map[string]any{"id": int64(1)}}
	p, err := checkAgainstManual(t, r, e, v)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if p.Values["name"] != "" {
		t.Fatalf("expected zero value \"\" for missing optional string column, got %v", p.Values["name"])
	}
}

// TestProjectMissingRequiredReject 缺失必填列时整体拒收。
func TestProjectMissingRequiredReject(t *testing.T) {
	r := NewRegistry()
	v, err := r.Register(baseSchema())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	e := Event{Version: v, Fields: map[string]any{"name": "alice"}}
	_, err = checkAgainstManual(t, r, e, v)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("expected ErrMissingRequired, got %v", err)
	}
}

// TestProjectConversion 类型转换：int->string 总成功，
// string->int 仅在内容为合法整数时成功。
func TestProjectConversion(t *testing.T) {
	r := NewRegistry()
	v, err := r.Register([]Column{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "score", Type: TypeInt},
		{Name: "tag", Type: TypeString},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	t.Run("string to int ok", func(t *testing.T) {
		e := Event{Version: v, Fields: map[string]any{"id": int64(1), "score": "42"}}
		p, err := checkAgainstManual(t, r, e, v)
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		if p.Values["score"] != int64(42) {
			t.Fatalf("expected 42, got %v", p.Values["score"])
		}
	})

	t.Run("int to string ok", func(t *testing.T) {
		e := Event{Version: v, Fields: map[string]any{"id": int64(1), "tag": int64(99)}}
		p, err := checkAgainstManual(t, r, e, v)
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		if p.Values["tag"] != "99" {
			t.Fatalf("expected \"99\", got %v", p.Values["tag"])
		}
	})

	t.Run("string to int invalid rejects whole event", func(t *testing.T) {
		e := Event{Version: v, Fields: map[string]any{"id": int64(1), "score": "abc", "tag": "x"}}
		_, err = checkAgainstManual(t, r, e, v)
		if !errors.Is(err, ErrConversionFailed) {
			t.Fatalf("expected ErrConversionFailed, got %v", err)
		}
	})

	t.Run("unsupported value type rejects", func(t *testing.T) {
		e := Event{Version: v, Fields: map[string]any{"id": int64(1), "score": 3.14}}
		_, err = checkAgainstManual(t, r, e, v)
		if !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("expected ErrInvalidValue, got %v", err)
		}
	})
}

// TestProjectDroppedColumns 已删除列（目标模式中不存在的字段）静默丢弃。
func TestProjectDroppedColumns(t *testing.T) {
	r := NewRegistry()
	v1, err := r.Register([]Column{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "legacy", Type: TypeString},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	v2, err := r.DropColumn(v1, "legacy")
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	e := Event{Version: v1, Fields: map[string]any{"id": int64(5), "legacy": "old-stuff"}}
	p, err := checkAgainstManual(t, r, e, v2)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if _, ok := p.Values["legacy"]; ok {
		t.Fatalf("dropped column leaked into projection: %v", p.Values)
	}
}

// TestProjectUnregisteredVersion 目标版本未注册时拒收。
func TestProjectUnregisteredVersion(t *testing.T) {
	r := NewRegistry()
	e := Event{Version: 999, Fields: map[string]any{"id": int64(1)}}
	_, err := r.Project(e, 999)
	t.Logf("event(version=%d) projected to unregistered target 999: %v", e.Version, err)
	if !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("expected ErrVersionNotFound, got %v", err)
	}
}
