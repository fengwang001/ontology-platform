package api

import (
	"errors"
	"strings"
	"testing"

	"ontology/acl"
)

func newService(t *testing.T) (*Service, acl.Subject) {
	t.Helper()
	store := acl.NewStore()
	u := acl.User("u")
	store.Grant(u, "name", acl.Read)
	store.Grant(u, "name", acl.Write)
	store.Grant(u, "title", acl.Read)
	return NewService(store), u
}

// TestWriteEnforcement 表驱动遍历 写入形态(创建/更新)×字段权限组合，
// 验证：含无权限字段必拒（哨兵错误含字段名）、物化计数器为 0 或全量。
func TestWriteEnforcement(t *testing.T) {
	cases := []struct {
		name      string
		op        string // "create" | "update"
		attrs     map[string]any
		wantErr   error
		wantField string
	}{
		{"创建-全部有权限", "create", map[string]any{"name": "n"}, nil, ""},
		{"创建-含无写权限字段", "create", map[string]any{"name": "n", "title": "t"}, ErrWriteDenied, "title"},
		{"创建-含未知字段", "create", map[string]any{"secret": 1}, ErrWriteDenied, "secret"},
		{"更新-全部有权限", "update", map[string]any{"name": "n2"}, nil, ""},
		{"更新-含无写权限字段", "update", map[string]any{"name": "n2", "title": "t"}, ErrWriteDenied, "title"},
		{"更新-仅无权限字段", "update", map[string]any{"title": "t"}, ErrWriteDenied, "title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, u := newService(t)
			if tc.op == "update" {
				if err := svc.Create(u, "o", map[string]any{"name": "orig"}); err != nil {
					t.Fatalf("预备创建失败: %v", err)
				}
			}
			before := svc.materialized
			var err error
			if tc.op == "create" {
				err = svc.Create(u, "o", tc.attrs)
			} else {
				err = svc.Update(u, "o", tc.attrs)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want errors.Is %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if svc.materialized-before != len(tc.attrs) {
					t.Fatalf("成功写入应全量物化：+%d, want %d", svc.materialized-before, len(tc.attrs))
				}
				return
			}
			if !strings.Contains(err.Error(), `"`+tc.wantField+`"`) {
				t.Fatalf("错误未指出字段名：%v, want 含 %q", err, tc.wantField)
			}
			if svc.materialized != before {
				t.Fatalf("被拒写入应零物化：计数 +%d", svc.materialized-before)
			}
			// 更新被拒时对象保持原样。
			if tc.op == "update" {
				obj, _ := svc.Get(u, "o")
				if obj["name"] != "orig" {
					t.Fatalf("被拒更新改动了对象：%v", obj)
				}
			}
		})
	}
}

// TestPredicateMatrix 遍历 主体可见性×谓词形态（gt/eq/isnull/not/and），
// 凡引用不可见列必整条拒绝（含列名），可见列正常求值。
func TestPredicateMatrix(t *testing.T) {
	svc, u := newService(t)
	if err := svc.Create(u, "o", map[string]any{"name": "n1"}); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	preds := map[string]Predicate{
		"gt-可见列":     {Op: "gt", Column: "name", Value: "a"},
		"eq-可见列":     {Op: "eq", Column: "name", Value: "n1"},
		"isnull-不可见": {Op: "isnull", Column: "secret"},
		"not-不可见":    {Op: "not", Inner: &Predicate{Op: "gt", Column: "secret", Value: 10}},
		"and-嵌套不可见": {Op: "and", Args: []Predicate{
			{Op: "eq", Column: "name", Value: "n1"},
			{Op: "isnull", Column: "secret"},
		}},
		"and-全可见": {Op: "and", Args: []Predicate{
			{Op: "eq", Column: "name", Value: "n1"},
			{Op: "not", Inner: &Predicate{Op: "isnull", Column: "title"}},
		}},
	}
	for name, pred := range preds {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Query(u, pred)
			invisible := strings.Contains(name, "不可见")
			if invisible {
				if !errors.Is(err, ErrPredicateDenied) {
					t.Fatalf("err=%v, want ErrPredicateDenied", err)
				}
				if !strings.Contains(err.Error(), `"secret"`) {
					t.Fatalf("错误未指出列名：%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("可见列谓词被拒：%v", err)
			}
		})
	}
}

// TestValidationAndProjection 参数校验与查询结果投影。
func TestValidationAndProjection(t *testing.T) {
	svc, u := newService(t)
	cases := []struct {
		name string
		err  error
	}{
		{"空主体", svc.Create(acl.User(""), "o", map[string]any{"name": "x"})},
		{"空对象id", svc.Create(u, "", map[string]any{"name": "x"})},
		{"空属性集", svc.Create(u, "o", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, ErrInvalidInput) {
				t.Fatalf("err=%v, want ErrInvalidInput", tc.err)
			}
		})
	}
	if err := svc.Create(u, "o", map[string]any{"name": "n1"}); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := svc.Update(u, "ghost", map[string]any{"name": "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在对象 err=%v, want ErrNotFound", err)
	}
	rows, err := svc.Query(u, Predicate{Op: "eq", Column: "name", Value: "n1"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("查询失败: rows=%d err=%v", len(rows), err)
	}
	if rows[0]["name"] != "n1" {
		t.Fatalf("投影内容错误：%v", rows[0])
	}
}
