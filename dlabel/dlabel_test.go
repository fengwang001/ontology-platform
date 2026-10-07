package dlabel

import (
	"errors"
	"testing"
)

func setupPlatform(t *testing.T) (*Platform, *MemoryAuditLog) {
	t.Helper()
	log := NewMemoryAuditLog(0)
	p := NewPlatform([]string{"root"}, WithAuditLog(log), WithRetainedVersions(50))
	must(t, p.RegisterObjectType("root", "Person", map[string]ValueKind{
		"age":    KindInt,
		"salary": KindInt,
		"name":   KindString,
		"flag":   KindBool,
	}))
	return p, log
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSmokeTagFlipAndRR(t *testing.T) {
	p, _ := setupPlatform(t)
	must(t, p.SetRule("root", Rule{
		ObjectType: "Person", Tag: "minor",
		Body: AttrAtom("age", OpLt, IntValue(18)),
	}))
	must(t, p.SetGrant("root", Grant{
		Subject: "alice", Tag: "minor", Read: EffectDeny, Write: EffectDeny,
		Visibility: EffectAllow, Attrs: []string{"*"},
	}))
	must(t, p.SetGrant("root", Grant{
		Subject: "alice", Tag: "adult", Read: EffectAllow, Write: EffectAllow,
		Visibility: EffectAllow, Attrs: []string{"*"},
	}))
	// adult 标签：age >= 18
	must(t, p.SetRule("root", Rule{
		ObjectType: "Person", Tag: "adult",
		Body: AttrAtom("age", OpGte, IntValue(18)),
	}))
	must(t, p.CreateInstance("root", "Person", "p1", map[string]Value{
		"age":    IntValue(20),
		"salary": IntValue(100),
		"name":   StringValue("Ann"),
	}))

	snap := p.Begin("alice")
	res, err := snap.ReadAttributes("alice", "Person", "p1", []string{"age", "salary"})
	must(t, err)
	if !res.Attrs["age"].Allowed {
		t.Fatal("adult should allow read age")
	}

	// 并发写导致标签翻转 adult -> minor
	must(t, p.WriteInstance("root", "Person", "p1", map[string]Value{"age": IntValue(10)}))
	res2, err := p.Begin("alice").ReadAttributes("alice", "Person", "p1", []string{"age"})
	must(t, err)
	if res2.Attrs["age"].Allowed {
		t.Fatal("minor must deny read age immediately after flip")
	}

	// 旧快照仍看到旧状态（可重复读）
	res3, err := snap.ReadAttributes("alice", "Person", "p1", []string{"age"})
	must(t, err)
	if !res3.Attrs["age"].Allowed || res3.Version != snap.Version() {
		t.Fatalf("repeatable read violated: %+v v=%d", res3.Attrs["age"], res3.Version)
	}

	carried, err := p.Begin("root").TagCarried("root", "Person", "p1", "minor")
	must(t, err)
	if !carried {
		t.Fatal("minor should be carried at current version")
	}

	if _, err := snap.TagCarried("alice", "Person", "p1", "minor"); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin must not see tag status: %v", err)
	}
}

func TestConflictMergeOrderIndependent(t *testing.T) {
	p, _ := setupPlatform(t)
	// 两个标签都恒为携带，一个允许一个拒绝 age。
	must(t, p.SetRule("root", Rule{ObjectType: "Person", Tag: "tAllow", Body: ConstAtom(true)}))
	must(t, p.SetRule("root", Rule{ObjectType: "Person", Tag: "tDeny", Body: ConstAtom(true)}))
	must(t, p.SetGrant("root", Grant{Subject: "bob", Tag: "tAllow", Read: EffectAllow, Visibility: EffectAllow, Attrs: []string{"age"}}))
	must(t, p.SetGrant("root", Grant{Subject: "bob", Tag: "tDeny", Read: EffectDeny, Visibility: EffectAllow, Attrs: []string{"age"}}))
	must(t, p.CreateInstance("root", "Person", "x", map[string]Value{"age": IntValue(1)}))

	for i := 0; i < 5; i++ {
		res, err := p.Begin("bob").ReadAttributes("bob", "Person", "x", []string{"age"})
		must(t, err)
		if res.Attrs["age"].Allowed {
			t.Fatal("deny-overrides must deny regardless of ordering")
		}
	}
}

func TestMissingAttrAndCycleAndSnapshot(t *testing.T) {
	p, _ := setupPlatform(t)
	err := p.SetRule("root", Rule{ObjectType: "Person", Tag: "bad", Body: AttrAtom("nope", OpEq, IntValue(1))})
	if !errors.Is(err, ErrMissingAttribute) {
		t.Fatalf("want missing attribute, got %v", err)
	}
	// 循环：a -> b -> a
	err = p.ReplaceRules("root", "Person", []Rule{
		{ObjectType: "Person", Tag: "a", Body: TagAtom("b")},
		{ObjectType: "Person", Tag: "b", Body: TagAtom("a")},
	})
	if !errors.Is(err, ErrCyclicRule) {
		t.Fatalf("want cyclic rule, got %v", err)
	}

	must(t, p.SetRule("root", Rule{ObjectType: "Person", Tag: "ok", Body: ConstAtom(true)}))
	must(t, p.CreateInstance("root", "Person", "x", map[string]Value{"age": IntValue(1)}))
	snap := p.Begin("root")
	for i := int64(0); i < 60; i++ {
		must(t, p.WriteInstance("root", "Person", "x", map[string]Value{"age": IntValue(i + 2)}))
	}
	_, err = snap.ReadAttributes("root", "Person", "x", []string{"age"})
	if !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("want snapshot unavailable, got %v", err)
	}
}

func TestDeniedWriteNoSideEffects(t *testing.T) {
	p, _ := setupPlatform(t)
	must(t, p.SetRule("root", Rule{ObjectType: "Person", Tag: "g", Body: ConstAtom(true)}))
	must(t, p.SetGrant("root", Grant{Subject: "u", Tag: "g", Read: EffectAllow, Write: EffectDeny, Visibility: EffectAllow, Attrs: []string{"age"}}))
	must(t, p.CreateInstance("root", "Person", "x", map[string]Value{"age": IntValue(1)}))
	v := p.CurrentVersion()
	err := p.WriteInstance("u", "Person", "x", map[string]Value{"age": IntValue(2)})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("want denied, got %v", err)
	}
	if p.CurrentVersion() != v {
		t.Fatal("denied write advanced the clock")
	}
	res, err := p.Begin("u").ReadAttributes("u", "Person", "x", []string{"age"})
	must(t, err)
	if got, _ := res.Attrs["age"].Value.Int(); got != 1 {
		t.Fatalf("denied write changed value: %d", got)
	}
}

func TestHiddenAttrDoesNotLeak(t *testing.T) {
	p, log := setupPlatform(t)
	// secret 标签由不可读属性 salary 驱动；对 alice，secret 只允许读 name。
	must(t, p.SetRule("root", Rule{ObjectType: "Person", Tag: "secret", Body: AttrAtom("salary", OpGt, IntValue(50))}))
	must(t, p.SetGrant("root", Grant{Subject: "alice", Tag: "secret", Read: EffectAllow, Visibility: EffectAllow, Attrs: []string{"name"}}))
	must(t, p.CreateInstance("root", "Person", "x", map[string]Value{
		"salary": IntValue(100), "name": StringValue("Ann"), "age": IntValue(30),
	}))
	snap := p.Begin("alice")
	res, err := snap.ReadAttributes("alice", "Person", "x", []string{"name", "salary", "age"})
	must(t, err)
	if !res.Attrs["name"].Allowed {
		t.Fatal("name should be allowed via secret tag")
	}
	if res.Attrs["salary"].Allowed || res.Attrs["age"].Allowed {
		t.Fatal("unrelated attrs must be denied")
	}
	if _, ok := res.Attrs["salary"].Value.Int(); ok {
		t.Fatal("denied attr value leaked into result")
	}
	// 对外日志不含 salary 取值（仅记录读取了哪些属性，且日志属于特权通道）。
	for _, e := range log.Entries() {
		if e.Subject == "alice" && e.Call == "ReadAttributes" {
			if e.Outputs == "" {
				t.Fatal("audit outputs missing")
			}
		}
	}
}
