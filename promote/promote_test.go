package promote_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/promote"
	"ontology/registry"
)

// full 是拥有全部（动作, 级名）权限的调用者。
func full() promote.Caller {
	acts := []registry.Action{registry.Push, registry.Promote, registry.Yank, registry.Alias}
	stages := []string{"dev", "staging", "release"}
	var perms []registry.Permission
	for _, a := range acts {
		for _, s := range stages {
			perms = append(perms, registry.Permission{Action: a, Stage: s})
		}
	}
	return promote.Caller{Permissions: perms}
}

func threeStages() []registry.Stage {
	return []registry.Stage{
		{Name: "dev", Immutable: false, S: 0, Required: nil, Trusted: map[string]bool{}},
		{Name: "staging", Immutable: true, S: 60, Required: []string{"test"},
			Trusted: map[string]bool{"ci": true}},
		{Name: "release", Immutable: true, S: 3600, Required: []string{"test", "scan"},
			Trusted: map[string]bool{"ci": true, "sec": true}},
	}
}

func TestConfigValidation(t *testing.T) {
	nine := make([]registry.Stage, 9)
	nine[0] = registry.Stage{Name: "dev", S: 0, Trusted: map[string]bool{}}
	for i := 1; i < 9; i++ {
		nine[i] = registry.Stage{Name: fmt.Sprintf("s%d", i), S: 1, Trusted: map[string]bool{}}
	}
	bad := [][]registry.Stage{
		nil,
		{threeStages()[0]},
		nine,
		{
			{Name: "dev", S: 1, Trusted: map[string]bool{}},
			{Name: "stg", S: 0, Trusted: map[string]bool{}},
		},
		{
			{Name: "dev", S: 0, Required: []string{"x"}, Trusted: map[string]bool{}},
			{Name: "stg", S: 0, Trusted: map[string]bool{}},
		},
		{
			{Name: "dev", S: 0, Trusted: map[string]bool{}},
			{Name: "stg", S: 0, Required: []string{"a", "a"}, Trusted: map[string]bool{}},
		},
		{
			{Name: "dev", S: 0, Trusted: map[string]bool{}},
			{Name: "dev", S: 0, Trusted: map[string]bool{}},
		},
		{
			{Name: "dev", S: 0, Trusted: map[string]bool{}},
			{Name: "stg", S: 0, Required: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
				Trusted: map[string]bool{}},
		},
	}
	for i, st := range bad {
		if _, err := promote.New(st); !errors.Is(err, promote.ErrInvalidParam) {
			t.Fatalf("case %d: want ErrInvalidParam, got %v", i, err)
		}
	}
}

func TestCanonicalScenario(t *testing.T) {
	r, err := promote.New(threeStages())
	if err != nil {
		t.Fatal(err)
	}
	c := full()
	must := func(step string, e error) {
		if e != nil {
			t.Fatalf("%s: %v", step, e)
		}
	}
	wantErr := func(step string, e error, target error, needle string) {
		if !errors.Is(e, target) {
			t.Fatalf("%s: want %v, got %v", step, target, e)
		}
		if needle != "" && (e == nil || !strings.Contains(e.Error(), needle)) {
			t.Fatalf("%s: error %v does not name %q", step, e, needle)
		}
	}

	must("push d1", r.Push(0, "app", "v1", "d1", c))
	must("attest d1 test", r.Attest(10, "d1", "test", "ci", 5000))
	wantErr("t59 dwell", r.Promote(59, "app", "v1", "dev", c), promote.ErrDwell, "")
	must("t60 promote", r.Promote(60, "app", "v1", "dev", c))
	if f, ok := r.First("staging", "app", "d1"); !ok || f != 60 {
		t.Fatalf("first staging d1=%d,%v want 60", f, ok)
	}

	must("push d2", r.Push(100, "app", "v1", "d2", c))
	wantErr("d2 missing proof before immutable",
		r.Promote(200, "app", "v1", "dev", c), promote.ErrProofMissing, "test")
	must("attest d2 test", r.Attest(210, "d2", "test", "ci", 9000))
	wantErr("d2 immutable", r.Promote(300, "app", "v1", "dev", c), promote.ErrImmutable, "")

	must("dev back to d1", r.Push(300, "app", "v1", "d1", c))
	wantErr("release 3659 dwell",
		r.Promote(3659, "app", "v1", "staging", c), promote.ErrDwell, "")
	wantErr("release 3660 missing scan",
		r.Promote(3660, "app", "v1", "staging", c), promote.ErrProofMissing, "scan")
	must("attest scan", r.Attest(3700, "d1", "scan", "sec", 4000))
	must("release 3999", r.Promote(3999, "app", "v1", "staging", c))

	r2, _ := promote.New(threeStages())
	must("p2 push", r2.Push(0, "app", "v1", "d1", c))
	must("p2 test", r2.Attest(10, "d1", "test", "ci", 5000))
	must("p2 staging", r2.Promote(60, "app", "v1", "dev", c))
	must("p2 scan", r2.Attest(3700, "d1", "scan", "sec", 4000))
	wantErr("scan exactly expired at 4000",
		r2.Promote(4000, "app", "v1", "staging", c), promote.ErrProofMissing, "scan")
}

func TestFirstNotRefreshedAndResidencyBoundary(t *testing.T) {
	r, _ := promote.New(threeStages())
	c := full()
	if err := r.Push(0, "a", "t", "d", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Attest(0, "d", "test", "ci", 10000); err != nil {
		t.Fatal(err)
	}
	if err := r.Promote(59, "a", "t", "dev", c); !errors.Is(err, promote.ErrDwell) {
		t.Fatalf("off-by-one: %v", err)
	}
	if err := r.Promote(60, "a", "t", "dev", c); err != nil {
		t.Fatalf("equal boundary: %v", err)
	}
	if f, _ := r.First("staging", "a", "d"); f != 60 {
		t.Fatalf("first=%d want 60", f)
	}
	if err := r.Push(200, "a", "t", "d", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Promote(200, "a", "t", "dev", c); err != nil {
		t.Fatalf("idempotent same digest: %v", err)
	}
	if f, _ := r.First("staging", "a", "d"); f != 60 {
		t.Fatalf("first refreshed to %d", f)
	}
}

func TestClockRollback(t *testing.T) {
	r, _ := promote.New(threeStages())
	c := full()
	if err := r.Push(10, "a", "t", "d", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(9, "a", "t2", "d2", c); !errors.Is(err, promote.ErrClockRollback) {
		t.Fatalf("want clock rollback, got %v", err)
	}
}

func TestPermissionAndStageErrors(t *testing.T) {
	r, _ := promote.New(threeStages())
	none := promote.Caller{}
	if err := r.Push(0, "a", "t", "d", none); !errors.Is(err, promote.ErrForbidden) {
		t.Fatalf("push: %v", err)
	}
	if err := r.Push(0, "a", "t", "d", full()); err != nil {
		t.Fatal(err)
	}
	if err := r.Promote(0, "a", "t", "release", full()); !errors.Is(err, promote.ErrNoNextStage) {
		t.Fatalf("no next: %v", err)
	}
	if err := r.Promote(0, "a", "t", "bogus", full()); !errors.Is(err, promote.ErrInvalidParam) {
		t.Fatalf("bad stage: %v", err)
	}
	if err := r.Promote(0, "a", "t", "dev", none); !errors.Is(err, promote.ErrForbidden) {
		t.Fatalf("promote perm: %v", err)
	}
	if err := r.Promote(0, "a", "ghost", "dev", full()); !errors.Is(err, promote.ErrSource) {
		t.Fatalf("missing source: %v", err)
	}
}

func TestTrustedAndRequiredOrder(t *testing.T) {
	r, _ := promote.New(threeStages())
	c := full()
	_ = r.Push(0, "a", "t", "d", c)
	_ = r.Attest(0, "d", "test", "robot", 10000)
	err := r.Promote(60, "a", "t", "dev", c)
	if !errors.Is(err, promote.ErrProofMissing) || !strings.Contains(err.Error(), "test") {
		t.Fatalf("untrusted signer: %v", err)
	}
	_ = r.Attest(60, "d", "test", "ci", 70)
	if err := r.Promote(60, "a", "t", "dev", c); err != nil {
		t.Fatalf("to staging: %v", err)
	}
	err = r.Promote(3660, "a", "t", "staging", c)
	if !errors.Is(err, promote.ErrProofMissing) || !strings.Contains(err.Error(), "test") {
		t.Fatalf("required first missing must be test (expired), got %v", err)
	}
}

func TestReplayRejectsAfterExpiryAndRevoke(t *testing.T) {
	r, _ := promote.New(threeStages())
	c := full()
	_ = r.Push(0, "a", "t", "d", c)
	_ = r.Attest(0, "d", "test", "ci", 500)
	if err := r.Promote(60, "a", "t", "dev", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Promote(500, "a", "t", "dev", c); !errors.Is(err, promote.ErrProofMissing) {
		t.Fatalf("replay after expiry: %v", err)
	}
	// 撤销 ci：后续晋级缺 test，但已晋级的标签不动。
	_ = r.Attest(501, "d", "test", "ci", 5000)
	if err := r.RevokeSigner(502, "ci"); err != nil {
		t.Fatal(err)
	}
	if err := r.Promote(503, "a", "t", "dev", c); !errors.Is(err, promote.ErrProofMissing) {
		t.Fatalf("after revoke: %v", err)
	}
	if d, err := r.Resolve("staging", "a", "t"); err != nil || d != "d" {
		t.Fatalf("promoted tag changed: %q %v", d, err)
	}
}

func TestTombstoneAndMutableReuse(t *testing.T) {
	r, _ := promote.New(threeStages())
	c := full()
	_ = r.Push(0, "a", "t", "d", c)
	_ = r.Attest(0, "d", "test", "ci", 100000)
	if err := r.Promote(60, "a", "t", "dev", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Yank(60, "staging", "a", "t", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Promote(60, "a", "t", "dev", c); !errors.Is(err, promote.ErrTagYanked) {
		t.Fatalf("tombstone same digest: %v", err)
	}
	if err := r.Yank(60, "dev", "a", "t", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(60, "a", "t", "d", c); err != nil {
		t.Fatalf("mutable reuse: %v", err)
	}
	if _, err := r.Resolve("staging", "a", "t"); !errors.Is(err, promote.ErrTagYanked) {
		t.Fatalf("resolve yanked: %v", err)
	}
	if _, err := r.Resolve("staging", "a", "nope"); !errors.Is(err, promote.ErrNotFound) {
		t.Fatalf("resolve missing: %v", err)
	}
}

func TestAliasBidirectionalClashAndReferenced(t *testing.T) {
	r, _ := promote.New(threeStages())
	c := full()
	_ = r.Push(0, "a", "v1", "d1", c)
	// 别名与现存标签同名 => 冲突。
	if err := r.SetAlias(0, "dev", "a", "v1", "v1", c); !errors.Is(err, promote.ErrAliasClash) {
		t.Fatalf("alias == tag: %v", err)
	}
	if err := r.SetAlias(0, "dev", "a", "stable", "v1", c); err != nil {
		t.Fatal(err)
	}
	// 用别名名作标签落标签 => 别名冲突。
	if err := r.Push(0, "a", "stable", "dx", c); !errors.Is(err, promote.ErrAliasClash) {
		t.Fatalf("tag == alias: %v", err)
	}
	// 别名指向的标签不可 Yank。
	if err := r.Yank(0, "dev", "a", "v1", c); !errors.Is(err, promote.ErrReferenced) {
		t.Fatalf("yank referenced: %v", err)
	}
	// 别名可改指：先建新标签，改指后旧标签可 Yank。
	_ = r.Push(0, "a", "v2", "d2", c)
	if err := r.SetAlias(0, "dev", "a", "stable", "v2", c); err != nil {
		t.Fatalf("retarget: %v", err)
	}
	if d, err := r.Resolve("dev", "a", "stable"); err != nil || d != "d2" {
		t.Fatalf("resolve retarget: %q %v", d, err)
	}
	if err := r.Yank(0, "dev", "a", "v1", c); err != nil {
		t.Fatalf("yank after retarget: %v", err)
	}
	// 别名不能指向已删除（可变级 Yank 后不存在）的标签。
	if err := r.SetAlias(0, "dev", "a", "other", "v1", c); !errors.Is(err, promote.ErrNotFound) {
		t.Fatalf("alias to deleted: %v", err)
	}
}

func TestRejectOrdering(t *testing.T) {
	// 参数非法 > 时钟回退：now 回退且 stage 非法 => 报参数非法。
	r, _ := promote.New(threeStages())
	c := full()
	_ = r.Push(10, "a", "t", "d", c)
	if err := r.Promote(5, "a", "t", "bogus", c); !errors.Is(err, promote.ErrInvalidParam) {
		t.Fatalf("invalid before clock: %v", err)
	}
	// 时钟回退 > 无下一级：末级 + 时钟回退 => 时钟。
	if err := r.Promote(5, "a", "t", "release", c); !errors.Is(err, promote.ErrClockRollback) {
		t.Fatalf("clock before no-next: %v", err)
	}
	// 无下一级 > 无权限。
	if err := r.Promote(10, "a", "t", "release", promote.Caller{}); !errors.Is(err, promote.ErrNoNextStage) {
		t.Fatalf("no-next before perm: %v", err)
	}
	// 无权限 > 源缺失。
	if err := r.Promote(10, "a", "t", "dev", promote.Caller{}); !errors.Is(err, promote.ErrForbidden) {
		t.Fatalf("perm before source: %v", err)
	}
	// 源缺失 > 驻留不足（新仓库：标签不存在）。
	if err := r.Promote(10, "a", "ghost", "dev", c); !errors.Is(err, promote.ErrSource) {
		t.Fatalf("source before dwell: %v", err)
	}
}
