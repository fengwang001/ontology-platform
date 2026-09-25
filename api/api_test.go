package api

import (
	"errors"
	"testing"

	"ontology/acl"
)

// setup 构建：root 全权限；alice 对 name/dept 读写，secret 完全无授权；
// 另有 g 组用户 bob（组仅授予 dept 读），并预置一个完整对象。
func setup(t *testing.T) (*Service, acl.Subject, acl.Subject, acl.Subject, string) {
	t.Helper()
	s := acl.NewStore()
	root, alice := acl.User("root"), acl.User("alice")
	bob, g := acl.User("bob"), acl.GroupSubject("g")
	for _, attr := range []string{"name", "dept", "secret"} {
		s.Grant(root, attr, acl.Read)
		s.Grant(root, attr, acl.Write)
	}
	for _, attr := range []string{"name", "dept"} {
		s.Grant(alice, attr, acl.Read)
		s.Grant(alice, attr, acl.Write)
	}
	s.AddMember("g", bob)
	s.Grant(g, "dept", acl.Read)
	svc := NewService(s, Schema{Required: []string{"name"}})
	id, err := svc.Create(root, map[string]any{"name": "ada", "dept": "eng", "secret": 7})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	return svc, root, alice, bob, id
}

func TestWriteEnforcement(t *testing.T) {
	// 遍历 调用者 × 动作(创建/更新) × 提交字段 × 默认策略(默认拒绝)。
	callers := []struct {
		name string
		who  func(svc *Service, root, alice, bob acl.Subject, id string) acl.Subject
	}{
		{"root", func(_ *Service, root, _, _ acl.Subject, _ string) acl.Subject { return root }},
		{"alice", func(_ *Service, _, alice, _ acl.Subject, _ string) acl.Subject { return alice }},
		{"bob", func(_ *Service, _, _, bob acl.Subject, _ string) acl.Subject { return bob }},
	}
	ops := []struct {
		name    string
		fields  map[string]any
		denied  bool
		deniedA string
	}{
		{"visible-only fields", map[string]any{"name": "x", "dept": "y"}, false, ""},
		{"contains secret", map[string]any{"name": "x", "secret": 1}, true, "secret"},
		{"only secret", map[string]any{"secret": 1}, true, "secret"},
	}
	// 谁应被拒：root 从不；alice/bob 在含 secret 时被拒。
	expectDenied := map[string]map[string]bool{
		"root":  {},
		"alice": {"contains secret": true, "only secret": true},
		"bob":   {"visible-only fields": true, "contains secret": true, "only secret": true},
	}

	for _, caller := range callers {
		for _, op := range ops {
			t.Run(caller.name+"/"+op.name, func(t *testing.T) {
				for _, mode := range []string{"create", "update"} {
					svc, root, alice, bob, id := setup(t)
					who := caller.who(svc, root, alice, bob, id)
					before := svc.Materialized()
					var err error
					if mode == "create" {
						_, err = svc.Create(who, cloneWithName(op.fields))
					} else {
						err = svc.Update(who, id, op.fields)
					}
					wantDenied := expectDenied[caller.name][op.name]
					if wantDenied {
						if !errors.Is(err, ErrWriteDenied) {
							t.Fatalf("%s: err = %v, want ErrWriteDenied", mode, err)
						}
						if svc.Materialized() != before {
							t.Fatalf("%s: materialized %d -> %d (partial write!)",
								mode, before, svc.Materialized())
						}
						if mode == "update" {
							got, _ := svc.Get(root, id)
							if got["name"] != "ada" || got["dept"] != "eng" || got["secret"] != 7 {
								t.Fatalf("object mutated after denied update: %v", got)
							}
						}
					} else if err != nil {
						t.Fatalf("%s: unexpected err %v", mode, err)
					}
				}
			})
		}
	}
}

func TestArgumentValidation(t *testing.T) {
	svc, root, _, _, _ := setup(t)
	cases := []struct {
		name string
		obj  map[string]any
	}{
		{"empty object", map[string]any{}},
		{"missing required", map[string]any{"dept": "eng"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := svc.Materialized()
			if _, err := svc.Create(root, tc.obj); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
			if svc.Materialized() != before {
				t.Fatalf("materialized on invalid create")
			}
		})
	}
	if _, err := svc.Get(root, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing err = %v", err)
	}
}

func TestPredicateProbesDenied(t *testing.T) {
	// 谓词形态 × 调用者：NOT(secret > 10) 与 secret IS NULL 都只是引用 secret，
	// 对无读权限者两种探针形态均整条拒绝并指出列名。
	shapes := []struct {
		name string
		cols []string
	}{
		{"NOT(secret>10)", []string{"secret"}},
		{"secret IS NULL", []string{"secret"}},
		{"mixed visible+invisible", []string{"name", "secret"}},
		{"visible only", []string{"name", "dept"}},
	}
	for _, tc := range shapes {
		t.Run(tc.name, func(t *testing.T) {
			svc, root, alice, bob, id := setup(t)
			_, errA := svc.Query(alice, tc.cols)
			_, errB := svc.Query(bob, tc.cols)
			_, errRoot := svc.Query(root, tc.cols)
			visibleOnly := tc.name == "visible only"
			if visibleOnly {
				if errA != nil || errB != nil || errRoot != nil {
					t.Fatalf("visible predicate denied: %v %v %v", errA, errB, errRoot)
				}
				return
			}
			if !errors.Is(errA, ErrPredicateDenied) || !errors.Is(errB, ErrPredicateDenied) {
				t.Fatalf("probe %s not denied: alice=%v bob=%v", tc.name, errA, errB)
			}
			if errRoot != nil {
				t.Fatalf("root probe denied: %v", errRoot)
			}
			// 拒绝查询不得产出任何行。
			svc2, _, _, _, _ := setup(t)
			if rows, _ := svc2.Query(alice, []string{"secret"}); rows != nil {
				t.Fatalf("denied query returned %d rows", len(rows))
			}
			_ = id
		})
	}
}

func TestQueryProjection(t *testing.T) {
	svc, _, alice, bob, _ := setup(t)
	rowsA, err := svc.Query(alice, []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 1 {
		t.Fatalf("rows = %d", len(rowsA))
	}
	if _, ok := rowsA[0]["secret"]; ok {
		t.Fatal("secret leaked into alice projection")
	}
	if rowsA[0]["name"] != "ada" || rowsA[0]["dept"] != "eng" {
		t.Fatalf("alice projection = %v", rowsA[0])
	}
	// bob 仅经组继承 dept 读权：name/secret 均缺席（部分可见视图）。
	rowsB, err := svc.Query(bob, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rowsB[0]["name"]; ok {
		t.Fatal("name visible to bob")
	}
	if rowsB[0]["dept"] != "eng" {
		t.Fatalf("bob projection = %v", rowsB[0])
	}
}

func cloneWithName(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out["name"] = "n"
	return out
}
