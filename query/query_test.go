package query

import (
	"errors"
	"reflect"
	"testing"

	"ontology/dls"
	"ontology/role"
)

func setupExample(t *testing.T) (*Engine, *role.Store) {
	t.Helper()
	store := role.NewStore()
	engine := NewEngine(store)

	must(t, engine.PutDoc("logs-1", "d1", map[string]role.Value{
		"team": "a", "level": int64(3), "secret": "k1",
	}))
	must(t, engine.PutDoc("logs-1", "d2", map[string]role.Value{
		"team": "b", "level": int64(5), "secret": "k2",
	}))
	must(t, engine.PutDoc("logs-1", "d3", map[string]role.Value{
		"team": "a", "level": int64(9),
	}))

	must(t, store.PutRole("R1", []role.Entry{{
		IndexPattern: "logs-*",
		Filter:       ptr(role.Term("team", "a")),
		Fields:       role.FieldAuth{Grant: []string{"team", "level"}},
	}}))
	must(t, store.PutRole("R2", []role.Entry{{
		IndexPattern: "logs-1",
		Filter:       ptr(role.Range("level", 5, 9)),
		Fields:       role.FieldAuth{Grant: []string{"*"}, Except: []string{"secret"}},
	}}))
	must(t, store.PutRole("R3", []role.Entry{{
		IndexPattern: "logs-1",
		Fields:       role.FieldAuth{Grant: []string{"secret"}},
	}}))
	return engine, store
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ptr(e role.Expr) *role.Expr { return &e }

func TestExampleR1Only(t *testing.T) {
	engine, store := setupExample(t)
	must(t, store.BindUser("u1", []string{"R1"}))

	res, err := engine.Search("u1", "logs-1", role.Term("team", "a"), 100)
	must(t, err)
	if res.Total != 2 || len(res.Docs) != 2 {
		t.Fatalf("Total=%d len=%d, want 2/2", res.Total, len(res.Docs))
	}
	if res.Docs[0].ID != "d1" || res.Docs[1].ID != "d3" {
		t.Fatalf("ids = %s,%s", res.Docs[0].ID, res.Docs[1].ID)
	}
	if _, leak := res.Docs[0].Fields["secret"]; leak {
		t.Fatal("secret field leaked")
	}

	// d2 文档侧不可见，与真不存在不可区分。
	if _, err := engine.Get("u1", "logs-1", "d2"); !errors.Is(err, role.ErrDocNotFound) {
		t.Fatalf("Get d2: %v", err)
	}

	buckets, err := engine.Agg("u1", "logs-1", "level")
	must(t, err)
	want := []Bucket{{Value: int64(3), Count: 1}, {Value: int64(9), Count: 1}}
	if !reflect.DeepEqual(buckets, want) {
		t.Fatalf("agg level = %v, want %v", buckets, want)
	}
	// 不可见字段聚合：空列表而非报错。
	got, err := engine.Agg("u1", "logs-1", "secret")
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("agg secret = %v, want empty", got)
	}
}

func TestExampleR1R2NotInvisibleField(t *testing.T) {
	engine, store := setupExample(t)
	must(t, store.BindUser("u", []string{"R1", "R2"}))

	// Not(Term(secret,"k1"))：secret 不可见 → Term 恒假 → Not 恒真。
	res, err := engine.Search("u", "logs-1", role.Not(role.Term("secret", "k1")), 100)
	must(t, err)
	if res.Total != 3 || len(res.Docs) != 3 {
		t.Fatalf("Total=%d len=%d, want 3/3", res.Total, len(res.Docs))
	}
	for _, doc := range res.Docs {
		if _, leak := doc.Fields["secret"]; leak {
			t.Fatalf("secret leaked in %s", doc.ID)
		}
	}
	if got, _ := engine.Agg("u", "logs-1", "secret"); len(got) != 0 {
		t.Fatalf("agg secret = %v, want empty", got)
	}
}

func TestExampleR1R3NilFilter(t *testing.T) {
	engine, store := setupExample(t)
	must(t, store.BindUser("v", []string{"R1", "R3"}))

	res, err := engine.Search("v", "logs-1", role.Term("secret", "k2"), 100)
	must(t, err)
	if res.Total != 1 || len(res.Docs) != 1 || res.Docs[0].ID != "d2" {
		t.Fatalf("search secret k2 = %+v", res)
	}

	doc, err := engine.Get("v", "logs-1", "d2")
	must(t, err)
	if doc.Fields["secret"] != "k2" || doc.Fields["team"] != "b" || doc.Fields["level"] != int64(5) {
		t.Fatalf("fields = %v", doc.Fields)
	}
}

func TestNoIndexNoMatchIndistinguishable(t *testing.T) {
	engine, store := setupExample(t)
	must(t, store.BindUser("u", []string{"R1", "R2"}))

	if _, err := engine.Search("u", "logs-9", role.Term("team", "a"), 10); !errors.Is(err, role.ErrNoPerm) {
		t.Fatalf("missing index: %v", err)
	}
	if _, err := engine.Search("u", "other", role.Term("team", "a"), 10); !errors.Is(err, role.ErrNoPerm) {
		t.Fatalf("unmatched index: %v", err)
	}
	if _, err := engine.Get("u", "logs-9", "d1"); !errors.Is(err, role.ErrNoPerm) {
		t.Fatalf("Get missing index: %v", err)
	}
	if _, err := engine.Agg("u", "other", "team"); !errors.Is(err, role.ErrNoPerm) {
		t.Fatalf("Agg unmatched index: %v", err)
	}
}

func TestErrorOrdering(t *testing.T) {
	engine, store := setupExample(t)
	must(t, store.BindUser("u", []string{"R1"}))

	// 参数非法先于用户不存在。
	if _, err := engine.Search("", "logs-1", role.Term("team", "a"), 10); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad user: %v", err)
	}
	// size 越界。
	if _, err := engine.Search("ghost", "logs-1", role.Term("team", "a"), 0); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("size 0: %v", err)
	}
	if _, err := engine.Search("ghost", "logs-1", role.Term("team", "a"), 1001); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("size 1001: %v", err)
	}
	// 非法表达式先于用户不存在。
	if _, err := engine.Search("ghost", "logs-1", role.Term("x", 3), 10); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad expr: %v", err)
	}
	// 用户不存在先于无权。
	if _, err := engine.Search("ghost", "logs-1", role.Term("team", "a"), 10); !errors.Is(err, role.ErrNoUser) {
		t.Fatalf("no user: %v", err)
	}
	// 无权先于文档不存在。
	must(t, store.BindUser("w", []string{"R1"}))
	if _, err := engine.Get("w", "other", "d1"); !errors.Is(err, role.ErrNoPerm) {
		t.Fatalf("no perm should precede doc missing: %v", err)
	}
	// 合法但文档不存在。
	if _, err := engine.Get("u", "logs-1", "zzz"); !errors.Is(err, role.ErrDocNotFound) {
		t.Fatalf("missing doc: %v", err)
	}
}

func TestAdminOpsValidation(t *testing.T) {
	engine, _ := setupExample(t)
	if err := engine.PutDoc("", "d1", nil); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad index: %v", err)
	}
	if err := engine.PutDoc("ix", "d1", map[string]role.Value{"": "x"}); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad field: %v", err)
	}
	if err := engine.PutDoc("ix", "d1", map[string]role.Value{"f": 3}); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("bad value type: %v", err)
	}
	if err := engine.DeleteDoc("", "d1"); !errors.Is(err, role.ErrInvalid) {
		t.Fatalf("delete bad index: %v", err)
	}
	// 删除真不存在的文档不报错。
	if err := engine.DeleteDoc("logs-1", "ghost"); err != nil {
		t.Fatalf("delete missing doc: %v", err)
	}
	// 索引在首次 PutDoc 时出现；DeleteDoc 不创建索引。
	must(t, engine.PutDoc("fresh", "d", map[string]role.Value{"f": "v"}))
}

func TestSizeCutoffAndIDSort(t *testing.T) {
	engine, store := setupExample(t)
	must(t, store.BindUser("u", []string{"R1", "R2", "R3"}))

	res, err := engine.Search("u", "logs-1", role.Not(role.Term("zzz", "x")), 2)
	must(t, err)
	if res.Total != 3 || len(res.Docs) != 2 || res.Docs[0].ID != "d1" || res.Docs[1].ID != "d2" {
		t.Fatalf("cutoff result = %+v", res)
	}
}

// TestTouchedIndependentOfGlobalRoles 证明 Get 的权限判定触碰条目数
// 只与该用户角色有关，与系统其他角色数（10 vs 10000）无关。
func TestTouchedIndependentOfGlobalRoles(t *testing.T) {
	for _, extra := range []int{10, 10000} {
		store := role.NewStore()
		engine := NewEngine(store)
		must(t, engine.PutDoc("ix", "d", map[string]role.Value{"f": int64(1)}))

		entries := []role.Entry{
			{IndexPattern: "a*", Fields: role.FieldAuth{Unrestricted: true}},
			{IndexPattern: "ix", Fields: role.FieldAuth{Unrestricted: true}},
		}
		must(t, store.PutRole("R", entries))
		for i := 0; i < extra; i++ {
			name := "X" + itoa(i)
			must(t, store.PutRole(name, []role.Entry{
				{IndexPattern: "other-*", Fields: role.FieldAuth{Grant: []string{"z"}}},
			}))
		}
		must(t, store.BindUser("u", []string{"R"}))

		groups, userTotal, err := store.UserEntries("u")
		must(t, err)
		view, err := dls.Resolve(groups, "ix")
		must(t, err)
		if view.Touched() != 2 || view.Touched() > userTotal || userTotal != 2 {
			t.Fatalf("extra=%d touched=%d userTotal=%d", extra, view.Touched(), userTotal)
		}
		if _, err := engine.Get("u", "ix", "d"); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
