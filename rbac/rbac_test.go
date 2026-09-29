package rbac

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func newTestManager(t *testing.T) (*Manager, *bytes.Buffer) {
	t.Helper()
	mgr := NewManager()
	var buf bytes.Buffer
	mgr.SetLogger(&buf)
	return mgr, &buf
}

// 并发求值同一主体：所有判定逐字段一致；在 -race 下并发读写也安全。
func TestConcurrentEvaluation(t *testing.T) {
	mgr, _ := newTestManager(t)
	mustCreateRoles(t, mgr, "viewer", "editor", "admin")
	must(t, mgr.GrantToRole("viewer", "read"))
	must(t, mgr.GrantToRole("editor", "write"))
	must(t, mgr.GrantToRole("admin", "delete"))
	must(t, mgr.AddInheritance("editor", "viewer"))
	must(t, mgr.AddInheritance("admin", "editor"))
	must(t, mgr.AssignRole("alice", "admin"))

	const goroutines = 64
	var wg sync.WaitGroup
	decisions := make([]Decision, goroutines)
	effective := make([]map[Permission][]string, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			decisions[i] = mgr.Evaluate("alice", "read")
			effective[i] = mgr.EffectivePermissions("alice")
		}(i)
	}
	wg.Wait()

	for i := 1; i < goroutines; i++ {
		if !reflect.DeepEqual(decisions[0], decisions[i]) {
			t.Fatalf("decision %d = %+v, want %+v", i, decisions[i], decisions[0])
		}
		if !reflect.DeepEqual(effective[0], effective[i]) {
			t.Fatalf("effective %d = %v, want %v", i, effective[i], effective[0])
		}
	}
}

// 并发混合求值与变更加撤销：不应数据竞争，最终状态保持一致。
func TestConcurrentMixedReadsAndWrites(t *testing.T) {
	mgr, _ := newTestManager(t)
	mustCreateRoles(t, mgr, "viewer")
	must(t, mgr.GrantToRole("viewer", "read"))
	must(t, mgr.AssignRole("alice", "viewer"))

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			mgr.Evaluate("alice", "read")
			mgr.EffectivePermissions("alice")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = mgr.GrantToSubject("alice", "tmp")
			_ = mgr.RevokeSubjectGrant("alice", "tmp")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = mgr.AssignRole("alice", "viewer")
			_ = mgr.RevokeRoleAssignment("alice", "viewer")
		}
	}()

	wg.Wait()

	// 撤销循环结束后重新赋予，保证最终状态确定且 read 仍可经角色判定。
	// 注意：assign/revoke 循环结束时角色可能已被撤销，这里按最终状态断言
	// 直接授权路径与角色路径二选一；为确定起见重新赋予角色。
	if err := mgr.AssignRole("alice", "viewer"); err != nil &&
		!errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	if d := mgr.Evaluate("alice", "read"); !d.Granted {
		t.Fatalf("final read evaluation denied: %+v", d)
	}
}

// 角色不存在、重复赋予、撤销不存在授权必须返回可区分错误，且拒绝无副作用。
func TestRejectionsDistinguishableAndAtomic(t *testing.T) {
	mgr, _ := newTestManager(t)
	mustCreateRoles(t, mgr, "viewer")
	must(t, mgr.GrantToRole("viewer", "read"))
	must(t, mgr.AssignRole("alice", "viewer"))

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"assign missing role", func() error { return mgr.AssignRole("x", "ghost") }, ErrRoleNotFound},
		{"grant missing role", func() error { return mgr.GrantToRole("ghost", "p") }, ErrRoleNotFound},
		{"inherit missing child", func() error { return mgr.AddInheritance("ghost", "viewer") }, ErrRoleNotFound},
		{"inherit missing parent", func() error { return mgr.AddInheritance("viewer", "ghost") }, ErrRoleNotFound},
		{"duplicate role", func() error { return mgr.CreateRole("viewer") }, ErrDuplicate},
		{"duplicate assignment", func() error { return mgr.AssignRole("alice", "viewer") }, ErrDuplicate},
		{"duplicate inheritance", func() error {
			mustCreateRoles(t, mgr, "child")
			must(t, mgr.AddInheritance("child", "viewer"))
			return mgr.AddInheritance("child", "viewer")
		}, ErrDuplicate},
		{"duplicate role grant", func() error { return mgr.GrantToRole("viewer", "read") }, ErrDuplicate},
		{"duplicate subject grant", func() error {
			must(t, mgr.GrantToSubject("alice", "personal"))
			return mgr.GrantToSubject("alice", "personal")
		}, ErrDuplicate},
		{"revoke missing assignment", func() error {
			return mgr.RevokeRoleAssignment("bob", "viewer")
		}, ErrGrantNotFound},
		{"revoke missing inheritance", func() error {
			mustCreateRoles(t, mgr, "lonely")
			return mgr.RemoveInheritance("lonely", "viewer")
		}, ErrGrantNotFound},
		{"revoke missing role grant", func() error {
			return mgr.RevokeRoleGrant("viewer", "nope")
		}, ErrGrantNotFound},
		{"revoke missing subject grant", func() error {
			return mgr.RevokeSubjectGrant("alice", "nope")
		}, ErrGrantNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	want := map[Permission][]string{
		"read":     {"role:viewer"},
		"personal": {"direct"},
	}
	if got := mgr.EffectivePermissions("alice"); !reflect.DeepEqual(got, want) {
		t.Fatalf("state changed after rejected ops: %v, want %v", got, want)
	}
}

// 同一组角色与授权以任意合法顺序注册，求值结果完全相同。
func TestRegistrationOrderIndependence(t *testing.T) {
	build := func(grantsFirst bool) map[Permission][]string {
		mgr, _ := newTestManager(t)
		mustCreateRoles(t, mgr, "viewer", "editor", "admin")
		if grantsFirst {
			must(t, mgr.GrantToRole("viewer", "read"))
			must(t, mgr.GrantToRole("editor", "write"))
			must(t, mgr.GrantToRole("admin", "delete"))
			must(t, mgr.AddInheritance("admin", "editor"))
			must(t, mgr.AddInheritance("editor", "viewer"))
			must(t, mgr.AssignRole("alice", "admin"))
		} else {
			must(t, mgr.AssignRole("alice", "admin"))
			must(t, mgr.AddInheritance("editor", "viewer"))
			must(t, mgr.AddInheritance("admin", "editor"))
			must(t, mgr.GrantToRole("admin", "delete"))
			must(t, mgr.GrantToRole("editor", "write"))
			must(t, mgr.GrantToRole("viewer", "read"))
		}
		must(t, mgr.GrantToSubject("alice", "read"))
		return mgr.EffectivePermissions("alice")
	}

	want := map[Permission][]string{
		"read":   {"direct"},
		"write":  {"role:editor"},
		"delete": {"role:admin"},
	}
	if got := build(true); !reflect.DeepEqual(got, want) {
		t.Fatalf("grants-first = %v, want %v", got, want)
	}
	if got := build(false); !reflect.DeepEqual(got, want) {
		t.Fatalf("assign-first = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(build(true), build(false)) {
		t.Fatal("results depend on registration order")
	}
}

// 直接主体授权优先于角色授权；撤销直接授权后回落到角色来源。
func TestDirectGrantPrecedence(t *testing.T) {
	mgr, _ := newTestManager(t)
	mustCreateRoles(t, mgr, "viewer")
	must(t, mgr.GrantToRole("viewer", "read"))
	must(t, mgr.AssignRole("alice", "viewer"))

	d := mgr.Evaluate("alice", "read")
	if !d.Granted || !reflect.DeepEqual(d.Sources, []string{"role:viewer"}) {
		t.Fatalf("before direct grant: %+v", d)
	}

	must(t, mgr.GrantToSubject("alice", "read"))
	d = mgr.Evaluate("alice", "read")
	if !d.Granted || !reflect.DeepEqual(d.Sources, []string{"direct"}) {
		t.Fatalf("after direct grant: %+v", d)
	}
	if got := mgr.EffectivePermissions("alice"); !reflect.DeepEqual(got["read"], []string{"direct"}) {
		t.Fatalf("effective read sources = %v, want [direct]", got["read"])
	}

	must(t, mgr.RevokeSubjectGrant("alice", "read"))
	d = mgr.Evaluate("alice", "read")
	if !d.Granted || !reflect.DeepEqual(d.Sources, []string{"role:viewer"}) {
		t.Fatalf("after revoke direct: %+v", d)
	}
}

// 权限并集：多角色与菱形继承取并集，共享祖先只贡献一次。
func TestPermissionUnion(t *testing.T) {
	mgr, _ := newTestManager(t)
	mustCreateRoles(t, mgr, "base", "left", "right", "extra")

	must(t, mgr.GrantToRole("base", "common"))
	must(t, mgr.GrantToRole("left", "l"))
	must(t, mgr.GrantToRole("right", "r"))
	must(t, mgr.GrantToRole("extra", "x"))
	must(t, mgr.AddInheritance("left", "base"))
	must(t, mgr.AddInheritance("right", "base"))

	must(t, mgr.AssignRole("alice", "left"))
	must(t, mgr.AssignRole("alice", "right"))
	must(t, mgr.AssignRole("alice", "extra"))

	want := map[Permission][]string{
		"common": {"role:base"},
		"l":      {"role:left"},
		"r":      {"role:right"},
		"x":      {"role:extra"},
	}
	if got := mgr.EffectivePermissions("alice"); !reflect.DeepEqual(got, want) {
		t.Fatalf("union = %v, want %v", got, want)
	}

	d := mgr.Evaluate("alice", "common")
	if !reflect.DeepEqual(d.Sources, []string{"role:base"}) {
		t.Fatalf("common should have single base source: %+v", d)
	}
}

// 循环继承：自环、直接环、间接环均被拒绝，且继承表不发生变化。
func TestCycleDetection(t *testing.T) {
	mgr, _ := newTestManager(t)
	mustCreateRoles(t, mgr, "a", "b", "c")

	if err := mgr.AddInheritance("a", "a"); !errors.Is(err, ErrCycle) {
		t.Fatalf("self inheritance err = %v, want ErrCycle", err)
	}

	must(t, mgr.AddInheritance("a", "b"))
	must(t, mgr.AddInheritance("b", "c"))

	if err := mgr.AddInheritance("c", "a"); !errors.Is(err, ErrCycle) {
		t.Fatalf("indirect cycle err = %v, want ErrCycle", err)
	}
	if err := mgr.AddInheritance("b", "a"); !errors.Is(err, ErrCycle) {
		t.Fatalf("direct cycle err = %v, want ErrCycle", err)
	}

	// 被拒绝的边不得生效：c 不应经环获得 a 的权限。
	must(t, mgr.GrantToRole("a", "root-only"))
	must(t, mgr.AssignRole("alice", "c"))
	if d := mgr.Evaluate("alice", "root-only"); d.Granted {
		t.Fatalf("rejected cycle edge leaked into hierarchy: %+v", d)
	}
}

func mustCreateRoles(t *testing.T, mgr *Manager, roles ...string) {
	t.Helper()
	for _, role := range roles {
		if err := mgr.CreateRole(role); err != nil {
			t.Fatalf("CreateRole(%q): %v", role, err)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// 多层继承：admin -> editor -> viewer，权限逐层向上取并集。
func TestMultiLevelInheritance(t *testing.T) {
	mgr, log := newTestManager(t)
	mustCreateRoles(t, mgr, "viewer", "editor", "admin")

	must(t, mgr.GrantToRole("viewer", "read"))
	must(t, mgr.GrantToRole("editor", "write"))
	must(t, mgr.GrantToRole("admin", "delete"))
	must(t, mgr.AddInheritance("editor", "viewer"))
	must(t, mgr.AddInheritance("admin", "editor"))
	must(t, mgr.AssignRole("alice", "admin"))

	want := map[Permission][]string{
		"read":   {"role:viewer"},
		"write":  {"role:editor"},
		"delete": {"role:admin"},
	}
	if got := mgr.EffectivePermissions("alice"); !reflect.DeepEqual(got, want) {
		t.Fatalf("admin effective permissions = %v, want %v", got, want)
	}

	for _, perm := range []Permission{"read", "write", "delete"} {
		if d := mgr.Evaluate("alice", perm); !d.Granted {
			t.Fatalf("alice %s: got denied, want allowed: %s", perm, d.Reason)
		}
	}

	// editor 不能越权删除；editor 经 viewer 获得 read。
	must(t, mgr.AssignRole("bob", "editor"))
	if d := mgr.Evaluate("bob", "delete"); d.Granted {
		t.Fatalf("bob delete: got allowed via %v, want denied", d.Sources)
	}
	if d := mgr.Evaluate("bob", "read"); !d.Granted ||
		!reflect.DeepEqual(d.Sources, []string{"role:viewer"}) {
		t.Fatalf("bob read: %+v", d)
	}

	// viewer 只能读。
	must(t, mgr.AssignRole("carol", "viewer"))
	if d := mgr.Evaluate("carol", "write"); d.Granted {
		t.Fatalf("carol write: got allowed, want denied")
	}

	// 日志必须包含主体、角色、权限与判定依据。
	logText := log.String()
	for _, wantFragment := range []string{
		`subject="alice" permission="read" decision=allow basis=roles`,
		`role="viewer" permission="read" result=ok`,
		`subject="carol" permission="write" decision=deny basis=none`,
	} {
		if !strings.Contains(logText, wantFragment) {
			t.Fatalf("log missing %q in:\n%s", wantFragment, logText)
		}
	}
}
