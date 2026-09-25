package api

import (
	"errors"
	"testing"

	"ontology/access"
	"ontology/acl"
)

func newService(t *testing.T) (*Service, acl.Subject) {
	t.Helper()
	pol := acl.NewPolicy()
	u := acl.User("u")
	g := acl.Group("g")
	pol.AddMember(g, u)
	pol.Grant(g, "name", acl.Read)
	pol.Grant(g, "name", acl.Write)
	pol.Grant(u, "age", acl.Read)
	pol.Grant(u, "age", acl.Write)
	return NewService(access.NewEvaluator(pol)), u
}

// TestWriteEnforcement 创建/更新 × 有权限/无权限：拒绝必指名、零静默丢弃。
func TestWriteEnforcement(t *testing.T) {
	cases := []struct {
		name       string
		op         string // "create" / "update"
		attrs      map[string]any
		wantErr    error
		wantFields []string
		wantDelta  int // 物化计数器增量：失败为 0，成功为全量
	}{
		{"创建全有权限", "create", map[string]any{"name": "a", "age": 1}, nil, nil, 2},
		{"创建含无权限字段", "create", map[string]any{"name": "a", "secret": 1},
			ErrWriteDenied, []string{"secret"}, 0},
		{"创建全部无权限", "create", map[string]any{"x": 1, "y": 2},
			ErrWriteDenied, []string{"x", "y"}, 0},
		{"更新全有权限", "update", map[string]any{"age": 2}, nil, nil, 1},
		{"更新含无权限字段", "update", map[string]any{"age": 3, "secret": 9},
			ErrWriteDenied, []string{"secret"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, u := newService(t)
			if tc.op == "update" {
				if err := svc.Create(u, "o", map[string]any{"name": "n"}); err != nil {
					t.Fatal(err)
				}
			}
			before := svc.Materialized()
			var err error
			if tc.op == "create" {
				err = svc.Create(u, "o", tc.attrs)
			} else {
				err = svc.Update(u, "o", tc.attrs)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("err=%v, want nil", err)
				}
			} else {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v, want errors.Is %v", err, tc.wantErr)
				}
				var dfe *DeniedFieldsError
				if !errors.As(err, &dfe) {
					t.Fatalf("err 应携带 DeniedFieldsError")
				}
				if len(dfe.Fields) != len(tc.wantFields) ||
					(len(tc.wantFields) > 0 && dfe.Fields[0] != tc.wantFields[0]) {
					t.Fatalf("Fields=%v, want %v", dfe.Fields, tc.wantFields)
				}
			}
			if got := svc.Materialized() - before; got != tc.wantDelta {
				t.Fatalf("物化增量=%d, want %d", got, tc.wantDelta)
			}
		})
	}
}

// TestPredicateProbes 遍历谓词形态×列可见性：引用不可见列必整条拒绝。
func TestPredicateProbes(t *testing.T) {
	cases := []struct {
		name       string
		pred       Predicate
		wantDenied bool
		wantColumn string
	}{
		{"可见列比较", Cmp{Column: "age", Op: ">", Value: 10}, false, ""},
		{"不可见列比较", Cmp{Column: "secret", Op: ">", Value: 10}, true, "secret"},
		{"NOT 探针", Not{Inner: Cmp{Column: "secret", Op: ">", Value: 10}}, true, "secret"},
		{"IS NULL 探针", IsNull{Column: "secret"}, true, "secret"},
		{"NOT IS NULL 探针", Not{Inner: IsNull{Column: "secret"}}, true, "secret"},
		{"AND 含不可见列", And{Parts: []Predicate{
			Cmp{Column: "age", Op: ">", Value: 1},
			Cmp{Column: "secret", Op: "=", Value: 1}}}, true, "secret"},
		{"OR 含不可见列", Or{Parts: []Predicate{
			IsNull{Column: "name"}, IsNull{Column: "hidden"}}}, true, "hidden"},
		{"AND 全部可见", And{Parts: []Predicate{
			Cmp{Column: "age", Op: ">", Value: 1},
			IsNull{Column: "name"}}}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, u := newService(t)
			if err := svc.Create(u, "o", map[string]any{"name": "n", "age": 30}); err != nil {
				t.Fatal(err)
			}
			_, err := svc.Query(u, tc.pred)
			if !tc.wantDenied {
				if err != nil {
					t.Fatalf("err=%v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrPredicateDenied) {
				t.Fatalf("err=%v, want errors.Is ErrPredicateDenied", err)
			}
			var pce *PredicateColumnError
			if !errors.As(err, &pce) || pce.Column != tc.wantColumn {
				t.Fatalf("column=%q, want %q", pce, tc.wantColumn)
			}
		})
	}
}

// TestInvalidArguments 参数校验与未找到均可 errors.Is 区分。
func TestInvalidArguments(t *testing.T) {
	svc, u := newService(t)
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"空 id", svc.Create(u, "", map[string]any{"name": "x"}), ErrInvalidArgument},
		{"空属性", svc.Create(u, "o", nil), ErrInvalidArgument},
		{"更新空属性", svc.Update(u, "o", nil), ErrInvalidArgument},
		{"更新不存在对象", svc.Update(u, "ghost", map[string]any{"name": "x"}), ErrNotFound},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("%s: err=%v, want errors.Is %v", tc.name, tc.err, tc.want)
		}
	}
	if err := svc.Create(u, "o", map[string]any{"name": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Create(u, "o", map[string]any{"name": "y"}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("重复创建: err=%v, want ErrInvalidArgument", err)
	}
}
