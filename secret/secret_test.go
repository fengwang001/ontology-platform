package secret

import (
	"errors"
	"testing"

	"ontology/scope"
)

func TestAddAndSetHappyPath(t *testing.T) {
	s := New()
	if err := s.AddRepo("r1", true, []string{"main", "release*"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEnv("r1", "prod", []string{"main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOrg("TOKEN", "o1", scope.All, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOrg("TOKEN", "o2", scope.All, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepo("r1", "TOKEN", "rv", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnv("r1", "prod", "KEY", "e1", false); err != nil {
		t.Fatal(err)
	}
	v := s.Snapshot()
	defer v.Release()
	if od, ok := v.LookupOrg("TOKEN"); !ok || od.Value != "o2" || od.Version != 2 {
		t.Fatalf("org version/value wrong: %+v ok=%v", od, ok)
	}
	if rd, ok := v.LookupRepo("r1", "TOKEN"); !ok || rd.Value != "rv" || rd.Version != 1 || !rd.ProtectedOnly {
		t.Fatalf("repo def wrong: %+v", rd)
	}
	if ed, ok := v.LookupEnv("r1", "prod", "KEY"); !ok || ed.Value != "e1" || ed.Version != 1 {
		t.Fatalf("env def wrong: %+v", ed)
	}
}

func TestVersionsStartAtOneAndIncrement(t *testing.T) {
	s := New()
	if err := s.AddRepo("r", false, nil); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := s.SetRepo("r", "K", "v", false); err != nil {
			t.Fatal(err)
		}
		v := s.Snapshot()
		d, ok := v.LookupRepo("r", "K")
		v.Release()
		if !ok || d.Version != i {
			t.Fatalf("version=%d want %d", d.Version, i)
		}
	}
}

func TestDelete(t *testing.T) {
	s := New()
	if err := s.AddRepo("r", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEnv("r", "e", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOrg("O", "v", scope.All, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepo("r", "R", "v", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnv("r", "e", "E", "v", false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOrg("O"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepoSecret("r", "R"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEnv("r", "e", "E"); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{s.DeleteOrg("O"), s.DeleteRepoSecret("r", "R"), s.DeleteEnv("r", "e", "E")} {
		if !errors.Is(err, ErrSecretNotFound) {
			t.Fatalf("want ErrSecretNotFound, got %v", err)
		}
	}
}

func TestRejectionOrder(t *testing.T) {
	s := New()
	// 参数非法先于任何存在性检查。
	if err := s.AddRepo("", false, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("AddRepo empty: %v", err)
	}
	if err := s.AddRepo("r", false, []string{"ba*d"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad pattern: %v", err)
	}
	if err := s.AddRepo("r", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRepo("r", false, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("dup repo: %v", err)
	}
	// AddEnv：非法 > 仓库不存在 > 已存在。
	if err := s.AddEnv("", "e", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("AddEnv invalid: %v", err)
	}
	if err := s.AddEnv("nope", "e", nil); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("AddEnv repo missing: %v", err)
	}
	if err := s.AddEnv("r", "e", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEnv("r", "e", nil); !errors.Is(err, ErrExists) {
		t.Fatalf("dup env: %v", err)
	}
	// SetEnv：非法 > 仓库不存在 > 环境不存在。
	if err := s.SetEnv("nope", "nope", "BAD-NAME", "v", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetEnv invalid ordering: %v", err)
	}
	if err := s.SetEnv("nope", "nope", "K", "v", false); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("SetEnv repo missing: %v", err)
	}
	if err := s.SetEnv("r", "nope", "K", "v", false); !errors.Is(err, ErrEnvNotFound) {
		t.Fatalf("SetEnv env missing: %v", err)
	}
	// SetRepo：非法 > 仓库不存在。
	if err := s.SetRepo("nope", "bad-name", "v", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetRepo invalid: %v", err)
	}
	if err := s.SetRepo("nope", "K", "v", false); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("SetRepo missing: %v", err)
	}
	// Delete：非法 > 仓库不存在 > 环境不存在 > 密钥不存在。
	if err := s.DeleteRepoSecret("nope", "bad-name"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Delete invalid: %v", err)
	}
	if err := s.DeleteRepoSecret("nope", "K"); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("Delete repo missing: %v", err)
	}
	if err := s.DeleteEnv("nope", "nope", "K"); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("DeleteEnv repo missing: %v", err)
	}
	if err := s.DeleteEnv("r", "nope", "K"); !errors.Is(err, ErrEnvNotFound) {
		t.Fatalf("DeleteEnv env missing: %v", err)
	}
	if err := s.DeleteRepoSecret("r", "MISSING"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Delete secret missing: %v", err)
	}
	// SetOrg 值/名非法。
	if err := s.SetOrg("bad-name", "v", scope.All, nil, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetOrg name: %v", err)
	}
	if err := s.SetOrg("K", "", scope.All, nil, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetOrg value: %v", err)
	}
	if err := s.SetOrg("K", "v", scope.Selected, []string{""}, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetOrg empty repo in list: %v", err)
	}
	if err := s.DeleteOrg("bad-name"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeleteOrg invalid: %v", err)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	s := New()
	if err := s.AddRepo("r", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepo("r", "K", "v1", false); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	d, _ := before.LookupRepo("r", "K")
	before.Release()
	// 非法 Set 不应使版本增长。
	if err := s.SetRepo("r", "K", "", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid, got %v", err)
	}
	after := s.Snapshot()
	d2, _ := after.LookupRepo("r", "K")
	after.Release()
	if d2 != d {
		t.Fatalf("state mutated by rejected Set: %+v != %+v", d2, d)
	}
}

func TestPatternSlicesCopied(t *testing.T) {
	s := New()
	pats := []string{"main"}
	if err := s.AddRepo("r", false, pats); err != nil {
		t.Fatal(err)
	}
	pats[0] = "*evil*"
	v := s.Snapshot()
	rv, ok := v.Repo("r")
	v.Release()
	if !ok || len(rv.Protected) != 1 || rv.Protected[0] != "main" {
		t.Fatalf("caller slice aliases store: %+v", rv)
	}
}
