package inject

import (
	"errors"
	"testing"

	"ontology/secret"
)

func exampleService(t *testing.T) *Service {
	t.Helper()
	s := secret.NewStore()
	must(t, s.AddRepo("r1", true, []string{"main", "release*"}))
	must(t, s.AddRepo("r2", false, nil))
	must(t, s.AddEnv("r1", "prod", []string{"main"}))
	must(t, s.SetOrg("TOKEN", "o1", secret.All, nil, false))
	must(t, s.SetOrg("DB", "o2", secret.Selected, []string{"r2"}, false))
	must(t, s.SetOrg("NPM", "o3", secret.Private, nil, false))
	must(t, s.SetRepo("r1", "TOKEN", "rv", true))
	must(t, s.SetEnv("r1", "prod", "KEY", "e1", false))
	return NewService(s)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func find(rs []Result, name string) Result {
	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}
	return Result{Name: name, Status: "missing"}
}

func TestSpecExampleMainPushProd(t *testing.T) {
	svc := exampleService(t)
	job := Job{
		Repo: "r1", Ref: "main", Event: Push, Env: "prod",
		Wants: []Want{
			{Name: "TOKEN", Required: true},
			{Name: "KEY", Required: true},
			{Name: "DB"},
			{Name: "NPM"},
		},
	}
	id, results, err := svc.Inject(job)
	if err != nil || id != 1 {
		t.Fatalf("Inject id=%d err=%v", id, err)
	}
	token := find(results, "TOKEN")
	if token.Status != StatusFound || token.Value != "rv" || token.Level != secret.RepoLevel {
		t.Fatalf("TOKEN = %+v", token)
	}
	key := find(results, "KEY")
	if key.Status != StatusFound || key.Value != "e1" || key.Level != secret.EnvLevel {
		t.Fatalf("KEY = %+v", key)
	}
	db := find(results, "DB")
	if db.Status != StatusNotFound {
		t.Fatalf("DB = %+v, want NotFound", db)
	}
	npm := find(results, "NPM")
	if npm.Status != StatusFound || npm.Value != "o3" || npm.Level != secret.OrgLevel {
		t.Fatalf("NPM = %+v", npm)
	}
}

func TestMostSpecificWithheldNoFallback(t *testing.T) {
	svc := exampleService(t)

	job := Job{Repo: "r1", Ref: "feat", Event: Push,
		Wants: []Want{{Name: "TOKEN"}}}
	_, results, err := svc.Inject(job)
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Status != StatusWithheld || r.Reason != ReasonProtected {
		t.Fatalf("TOKEN = %+v, want Withheld(Protected)", r)
	}

	_, _, err = svc.Inject(Job{Repo: "r1", Ref: "feat", Event: Push,
		Wants: []Want{{Name: "TOKEN", Required: true}}})
	var ie *InjectError
	if !errors.As(err, &ie) || ie.Name != "TOKEN" || ie.Status != StatusWithheld || ie.Reason != ReasonProtected {
		t.Fatalf("required err = %v (%T)", err, err)
	}
	if !errors.Is(err, ErrProtected) {
		t.Fatalf("errors.Is ErrProtected failed: %v", err)
	}

	// Failed required inject must not consume a job id (fresh service).
	svc2 := exampleService(t)
	_, _, err = svc2.Inject(Job{Repo: "r1", Ref: "feat", Event: Push,
		Wants: []Want{{Name: "TOKEN", Required: true}}})
	if err == nil {
		t.Fatal("expected failure")
	}
	id, _, err := svc2.Inject(Job{Repo: "r1", Ref: "main", Event: Push,
		Wants: []Want{{Name: "NPM"}}})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("failed inject consumed an id, next id = %d", id)
	}
}

func TestPREventsNotProtectedOnProtectedName(t *testing.T) {
	svc := exampleService(t)
	for _, ev := range []Event{PRInternal, PRFork} {
		_, results, err := svc.Inject(Job{Repo: "r1", Ref: "main", Event: ev,
			Wants: []Want{{Name: "TOKEN"}}})
		if err != nil {
			t.Fatalf("event %s: %v", ev, err)
		}
		r := results[0]
		if ev == PRInternal {
			if r.Status != StatusWithheld || r.Reason != ReasonProtected {
				t.Fatalf("%s TOKEN on protected-named ref: %+v", ev, r)
			}
		} else {
			if r.Status != StatusWithheld || r.Reason != ReasonFork {
				t.Fatalf("%s should withhold TOKEN as fork: %+v", ev, r)
			}
		}
	}
}

func TestForkDoesNotReportNotFound(t *testing.T) {
	svc := exampleService(t)
	_, results, err := svc.Inject(Job{Repo: "r1", Ref: "x", Event: PRFork,
		Wants: []Want{{Name: "NPM"}, {Name: "NOPE"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status != StatusWithheld || r.Reason != ReasonFork {
			t.Fatalf("fork result = %+v", r)
		}
	}
}

func TestEnvNotAllowedBeforeFork(t *testing.T) {
	svc := exampleService(t)
	// release-2 is protected for the repo but prod allows only main.
	_, _, err := svc.Inject(Job{Repo: "r1", Ref: "release-2", Event: Push, Env: "prod",
		Wants: []Want{{Name: "TOKEN"}}})
	if !errors.Is(err, ErrEnvNotAllowed) {
		t.Fatalf("push err = %v", err)
	}
	// Env check applies to every event, including fork.
	_, _, err = svc.Inject(Job{Repo: "r1", Ref: "release-2", Event: PRFork, Env: "prod",
		Wants: []Want{{Name: "TOKEN"}}})
	if !errors.Is(err, ErrEnvNotAllowed) {
		t.Fatalf("fork err = %v, want env-not-allowed before fork withholding", err)
	}
}

func TestPrecheckOrder(t *testing.T) {
	svc := exampleService(t)
	_, _, err := svc.Inject(Job{Repo: "ghost", Ref: "main", Event: Push, Wants: []Want{{Name: "bad-name"}}})
	if !errors.Is(err, secret.ErrInvalidArgument) {
		t.Fatalf("invalid beats missing repo: %v", err)
	}
	_, _, err = svc.Inject(Job{Repo: "ghost", Ref: "main", Event: Push, Wants: []Want{{Name: "A"}}})
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("missing repo: %v", err)
	}
	_, _, err = svc.Inject(Job{Repo: "r1", Ref: "main", Event: Push, Env: "ghost",
		Wants: []Want{{Name: "A"}}})
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("missing env: %v", err)
	}
	_, _, err = svc.Inject(Job{Repo: "r1", Ref: "main", Event: Push,
		Wants: []Want{{Name: "A"}, {Name: "A"}}})
	if !errors.Is(err, secret.ErrInvalidArgument) {
		t.Fatalf("dup wants: %v", err)
	}
	_, _, err = svc.Inject(Job{Repo: "r1", Ref: "main", Event: "weird",
		Wants: []Want{{Name: "A"}}})
	if !errors.Is(err, secret.ErrInvalidArgument) {
		t.Fatalf("bad event: %v", err)
	}
}

func TestRequiredFirstFailure(t *testing.T) {
	svc := exampleService(t)
	_, results, err := svc.Inject(Job{Repo: "r1", Ref: "main", Event: Push,
		Wants: []Want{{Name: "NPM"}, {Name: "MISS1", Required: true}, {Name: "MISS2", Required: true}}})
	var ie *InjectError
	if !errors.As(err, &ie) || ie.Name != "MISS1" || ie.Status != StatusNotFound {
		t.Fatalf("err = %v, results = %+v", err, results)
	}
	if !errors.Is(err, ErrMissingSecret) {
		t.Fatalf("errors.Is ErrMissingSecret: %v", err)
	}
}

func TestSnapshotSurvivesRotation(t *testing.T) {
	svc := exampleService(t)
	must(t, svc.store.SetOrg("ROTX", "oldsecret9", secret.Private, nil, false))
	id, _, err := svc.Inject(Job{Repo: "r1", Ref: "main", Event: Push,
		Wants: []Want{{Name: "ROTX"}}})
	if err != nil {
		t.Fatal(err)
	}
	must(t, svc.store.SetOrg("ROTX", "newsecret9", secret.Private, nil, false))
	out, err := svc.Mask(id, "old=oldsecret9 new=newsecret9")
	if err != nil {
		t.Fatal(err)
	}
	if out != "old=*** new=newsecret9" {
		t.Fatalf("mask after rotation = %q", out)
	}
}

func TestMaskMergeAndShortValues(t *testing.T) {
	cases := []struct {
		name        string
		values      []string
		line        string
		want        string
		noFullValue bool
	}{
		{"overlap", []string{"abcd", "cdef"}, "abcdef", "***", true},
		{"self overlap", []string{"abab"}, "xababab", "x***", true},
		{"adjacent", []string{"abcd"}, "abcdabcd", "***", true},
		{"separated", []string{"abcd"}, "abcd-abcd", "***-***", true},
		{"short value ignored", []string{"abc"}, "abc", "abc", false},
		{"exactly four", []string{"abcd"}, "xabcd", "x***", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(secret.NewStore())
			must(t, svc.store.AddRepo("r", false, nil))
			wants := make([]Want, len(tc.values))
			for i, v := range tc.values {
				wants[i] = Want{Name: secretName(i)}
				_ = svc.store.SetRepo("r", wants[i].Name, v, false)
			}
			id, _, err := svc.Inject(Job{Repo: "r", Ref: "b", Event: Push, Wants: wants})
			if err != nil {
				t.Fatal(err)
			}
			got, err := svc.Mask(id, tc.line)
			if err != nil || got != tc.want {
				t.Fatalf("Mask = %q, err=%v, want %q", got, err, tc.want)
			}
			if tc.noFullValue && containsAny(got, tc.values) {
				t.Fatalf("output %q still contains an injected value", got)
			}
		})
	}

	if _, err := NewService(secret.NewStore()).Mask(999, "x"); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("unknown job = %v", err)
	}
}

func secretName(i int) string {
	const digits = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	name := "N"
	n := i
	for {
		name += string(digits[n%36])
		n /= 36
		if n == 0 {
			break
		}
	}
	return name
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if len(sub) >= 4 && indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestProbeBudgetVsSecretCount(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := secret.NewStore()
		must(t, s.AddRepo("r", false, nil))
		for i := 0; i < n; i++ {
			must(t, s.SetOrg(secretName(i), "org-value", secret.All, nil, false))
		}
		svc := NewService(s)
		_, _, err := svc.Inject(Job{Repo: "r", Ref: "b", Event: Push,
			Wants: []Want{{Name: "VA"}, {Name: "NOPE"}, {Name: "VB"}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := svc.lastProbes(); got > 9 {
			t.Fatalf("n=%d probes=%d, want <= 3 per name", n, got)
		}

		_, _, err = svc.Inject(Job{Repo: "r", Ref: "b", Event: PRFork,
			Wants: []Want{{Name: "VA"}, {Name: "NOPE"}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := svc.lastProbes(); got != 0 {
			t.Fatalf("n=%d fork probes=%d, want 0", n, got)
		}
	}
}
