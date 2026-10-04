package inject

import (
	"errors"
	"testing"

	"ontology/scope"
	"ontology/secret"
)

func newFixture(t *testing.T) (*secret.Store, *Service) {
	t.Helper()
	s := secret.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AddRepo("r1", true, []string{"main", "release*"}))
	must(s.AddRepo("r2", false, []string{"main"}))
	must(s.AddEnv("r1", "prod", []string{"main"}))
	must(s.AddEnv("r1", "any", nil))
	must(s.SetOrg("TOKEN", "o1", scope.All, nil, false))
	must(s.SetOrg("DB", "o2", scope.Selected, []string{"r2"}, false))
	must(s.SetOrg("NPM", "o3", scope.Private, nil, false))
	must(s.SetOrg("PROT", "op", scope.All, nil, true))
	must(s.SetRepo("r1", "TOKEN", "rv", true))
	must(s.SetEnv("r1", "prod", "KEY", "env1", false))
	return s, New(s)
}

func w(name string, required bool) Want { return Want{Name: name, Required: required} }

func TestExampleScenario(t *testing.T) {
	_, svc := newFixture(t)
	out, err := svc.Inject(Job{"r1", "main", "push", "prod", []Want{
		w("TOKEN", true), w("KEY", true), w("DB", false), w("NPM", false),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.JobID != 1 || len(out.Results) != 4 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	checkFound := func(i int, val string, lvl Level, ver int) {
		t.Helper()
		f := out.Results[i].Found
		if f == nil || f.Value != val || f.Level != lvl || f.Version != ver {
			t.Fatalf("result %d = %+v", i, out.Results[i])
		}
	}
	checkFound(0, "rv", LevelRepo, 1)
	checkFound(1, "env1", LevelEnv, 1)
	if r := out.Results[2]; r.Found != nil || r.Withheld != nil {
		t.Fatalf("DB should be NotFound for r1: %+v", r)
	}
	checkFound(3, "o3", LevelOrg, 1)
}

func TestMostSpecificWithheldNoFallback(t *testing.T) {
	_, svc := newFixture(t)
	out, err := svc.Inject(Job{"r1", "feat", "push", "", []Want{w("TOKEN", false)}})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(out.Results[0].Withheld, ErrWithheldProtect) || out.Results[0].Found != nil {
		t.Fatalf("want Withheld(Protected), got %+v", out.Results[0])
	}
	out, err = svc.Inject(Job{"r1", "feat", "push", "", []Want{w("PROT", false)}})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(out.Results[0].Withheld, ErrWithheldProtect) {
		t.Fatalf("org protected-only should withhold: %+v", out.Results[0])
	}
	out, err = svc.Inject(Job{"r1", "main", "push", "", []Want{w("TOKEN", false)}})
	if err != nil {
		t.Fatal(err)
	}
	if f := out.Results[0].Found; f == nil || f.Value != "rv" || f.Level != LevelRepo {
		t.Fatalf("protected ref should find repo secret: %+v", out.Results[0])
	}
}

func TestPrEventsNeverProtected(t *testing.T) {
	_, svc := newFixture(t)
	out, err := svc.Inject(Job{"r1", "main", "pr_internal", "", []Want{w("TOKEN", false)}})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(out.Results[0].Withheld, ErrWithheldProtect) {
		t.Fatalf("pr_internal on main is still unprotected: %+v", out.Results[0])
	}
	out, err = svc.Inject(Job{"r1", "main", "pr_fork", "", []Want{w("TOKEN", false)}})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(out.Results[0].Withheld, ErrWithheldFork) {
		t.Fatalf("pr_fork: %+v", out.Results[0])
	}
}

func TestForkWithholdsEverythingAndSkipsStorage(t *testing.T) {
	_, svc := newFixture(t)
	out, err := svc.Inject(Job{"r1", "x", "pr_fork", "", []Want{w("NPM", false), w("NOPE", false)}})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range out.Results {
		if !errors.Is(r.Withheld, ErrWithheldFork) {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	if svc.probes() != 0 {
		t.Fatalf("fork probes = %d, want 0", svc.probes())
	}
	masked, err := svc.Mask(out.JobID, "o3 o3")
	if err != nil || masked != "o3 o3" {
		t.Fatalf("fork snapshot must be empty: %q, %v", masked, err)
	}
}

func TestEnvDeniedBeforeFork(t *testing.T) {
	_, svc := newFixture(t)
	_, err := svc.Inject(Job{"r1", "release-2", "push", "prod", []Want{w("TOKEN", true)}})
	if !errors.Is(err, ErrEnvDenied) {
		t.Fatalf("push release-2 prod: %v", err)
	}
	_, err = svc.Inject(Job{"r1", "x", "pr_fork", "prod", []Want{w("TOKEN", true)}})
	if !errors.Is(err, ErrEnvDenied) {
		t.Fatalf("env denied must precede fork withholding: %v", err)
	}
	out, err := svc.Inject(Job{"r1", "x", "push", "any", []Want{w("MISSING", false)}})
	if err != nil || out.Results[0].Found != nil || out.Results[0].Withheld != nil {
		t.Fatalf("env with empty branches should allow any ref: %+v err=%v", out, err)
	}
}
