package inject

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/secret"
)

func TestConcurrentAccess(t *testing.T) {
	store := secret.New()
	if err := store.AddRepo("r", true, []string{"main"}); err != nil {
		t.Fatal(err)
	}
	svc := New(store)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = store.SetRepo("r", fmt.Sprintf("K%d", g%4), "vvvv", false)
				_, _ = svc.Inject(Job{"r", "main", "push", "", []Want{{Name: "K0", Required: false}}})
				if g == 0 && i%50 == 0 {
					_, _ = svc.Mask(1, "vvvv")
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestGlobalCheckOrder(t *testing.T) {
	_, svc := newFixture(t)
	cases := []struct {
		name string
		job  Job
		want error
	}{
		{"bad event", Job{"r1", "main", "tag", "prod", []Want{w("K", true)}}, ErrInvalid},
		{"bad want name", Job{"r1", "main", "push", "prod", []Want{w("k", true)}}, ErrInvalid},
		{"dup wants", Job{"r1", "main", "push", "prod", []Want{w("K", true), w("K", false)}}, ErrInvalid},
		{"empty wants", Job{"r1", "main", "push", "prod", nil}, ErrInvalid},
		{"65 wants", Job{"r1", "main", "push", "", manyWants(65, false)}, ErrInvalid},
		{"empty ref", Job{"r1", "", "push", "prod", []Want{w("K", true)}}, ErrInvalid},
		{"repo missing", Job{"zzz", "main", "push", "", []Want{w("K", true)}}, ErrRepoNotFound},
		{"env missing", Job{"r1", "main", "push", "zzz", []Want{w("K", true)}}, ErrEnvNotFound},
		{"env denied", Job{"r1", "feat", "push", "prod", []Want{w("K", true)}}, ErrEnvDenied},
	}
	for _, c := range cases {
		_, err := svc.Inject(c.job)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func manyWants(n int, required bool) []Want {
	out := make([]Want, n)
	for i := range out {
		out[i] = Want{Name: fmt.Sprintf("K%02d", i), Required: required}
	}
	return out
}

func TestRequiredFailureFirstAndNoIdConsumed(t *testing.T) {
	_, svc := newFixture(t)
	var f *Failure
	_, err := svc.Inject(Job{"r1", "feat", "push", "", []Want{
		w("TOKEN", false), w("NOPE", true),
	}})
	if !errors.As(err, &f) || f.Name != "NOPE" || !errors.Is(err, ErrNotFound) {
		t.Fatalf("want NotFound failure on NOPE, got %v", err)
	}
	_, err = svc.Inject(Job{"r1", "feat", "push", "", []Want{w("TOKEN", true)}})
	if !errors.As(err, &f) || f.Name != "TOKEN" || !errors.Is(err, ErrWithheldProtect) {
		t.Fatalf("want protected failure on TOKEN, got %v", err)
	}
	_, err = svc.Inject(Job{"r1", "x", "pr_fork", "", []Want{w("NPM", true)}})
	if !errors.As(err, &f) || f.Name != "NPM" || !errors.Is(err, ErrWithheldFork) {
		t.Fatalf("want fork failure, got %v", err)
	}
	_, err = svc.Inject(Job{"r1", "feat", "push", "", []Want{w("NOPE", true), w("TOKEN", true)}})
	if !errors.As(err, &f) || f.Name != "NOPE" {
		t.Fatalf("first failed required name: %v", err)
	}
	out, err := svc.Inject(Job{"r1", "main", "push", "prod", []Want{w("KEY", false)}})
	if err != nil || out.JobID != 1 {
		t.Fatalf("failed injects must not consume ids: id=%d err=%v", out.JobID, err)
	}
}

func TestSnapshotSurvivesRotation(t *testing.T) {
	store, svc := newFixture(t)
	out, err := svc.Inject(Job{"r1", "main", "push", "prod", []Want{w("KEY", true)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnv("r1", "prod", "KEY", "e2-rotated", false); err != nil {
		t.Fatal(err)
	}
	masked, err := svc.Mask(out.JobID, "pre env1 post e2-rotated")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(masked, "env1") {
		t.Fatalf("old value must be masked: %q", masked)
	}
	if !strings.Contains(masked, "e2-rotated") {
		t.Fatalf("new value must remain visible for old job: %q", masked)
	}
	out2, err := svc.Inject(Job{"r1", "main", "push", "prod", []Want{w("KEY", true)}})
	if err != nil || out2.JobID != 2 {
		t.Fatalf("second job id: %v %v", out2, err)
	}
	if m2, _ := svc.Mask(out2.JobID, "e2-rotated"); m2 != "***" {
		t.Fatalf("new job masks new value: %q", m2)
	}
	if err := store.DeleteEnv("r1", "prod", "KEY"); err != nil {
		t.Fatal(err)
	}
	if m3, err := svc.Mask(out.JobID, "env1"); err != nil || m3 != "***" {
		t.Fatalf("delete must not change old snapshot: %q %v", m3, err)
	}
}

func TestMaskMerging(t *testing.T) {
	_, svc := newFixture(t)
	id := svc.newJobWithValues("abcd", "cdef")
	cases := []struct{ line, want string }{
		{"abcdef", "***"},
		{"zabcd", "z***"},
		{"ab", "ab"},
	}
	for _, c := range cases {
		got, err := svc.Mask(id, c.line)
		if err != nil || got != c.want {
			t.Errorf("Mask(%q)=%q,%v want %q", c.line, got, err, c.want)
		}
	}
	if id2 := svc.newJobWithValues("abab"); true {
		got, _ := svc.Mask(id2, "xababab")
		if got != "x***" {
			t.Fatalf("overlapping occurrences: %q", got)
		}
	}
	id3 := svc.newJobWithValues("abcd")
	if got, _ := svc.Mask(id3, "abcdabcd"); got != "***" {
		t.Fatalf("abutting occurrences should merge: %q", got)
	}
	if got, _ := svc.Mask(id3, "abcd-abcd"); got != "***-***" {
		t.Fatalf("separated occurrences: %q", got)
	}
	id4 := svc.newJobWithValues("abc")
	if got, _ := svc.Mask(id4, "abc"); got != "abc" {
		t.Fatalf("short values must not mask: %q", got)
	}
	if _, err := svc.Mask(9999, "x"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing job: %v", err)
	}
}

func TestProbeBudgetIndependentOfStoreSize(t *testing.T) {
	for _, n := range []int{100, 10000} {
		store := secret.New()
		if err := store.AddRepo("r", false, nil); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("K%05d", i)
			if err := store.SetRepo("r", name, "v"+name, false); err != nil {
				t.Fatal(err)
			}
		}
		svc := New(store)
		out, err := svc.Inject(Job{"r", "main", "push", "", []Want{
			{Name: "K00000", Required: false},
			{Name: "MISSING", Required: false},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if out.Results[0].Found == nil || out.Results[0].Found.Value != "vK00000" {
			t.Fatalf("n=%d first result wrong: %+v", n, out.Results[0])
		}
		if out.Results[1].Found != nil || out.Results[1].Withheld != nil {
			t.Fatalf("n=%d missing result wrong: %+v", n, out.Results[1])
		}
		// 两个名字：首个 repo 层命中（1 次），缺失者 repo+org（2 次），合计 3；
		// 且每名字都不超过 3，与库内密钥总数无关。
		if probes := svc.probes(); probes != 3 || probes > 3*len(out.Results) {
			t.Fatalf("n=%d probes=%d want 3 (≤ %d)", n, probes, 3*len(out.Results))
		}
	}
}

func BenchmarkProbes100(b *testing.B) { benchmarkProbes(b, 100) }
func BenchmarkProbes10000(b *testing.B) {
	if testing.Short() {
		b.Skip("10000-key benchmark skipped in -short")
	}
	benchmarkProbes(b, 10000)
}

func benchmarkProbes(b *testing.B, n int) {
	store := secret.New()
	if err := store.AddRepo("r", false, nil); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := store.SetRepo("r", fmt.Sprintf("K%05d", i), "v", false); err != nil {
			b.Fatal(err)
		}
	}
	svc := New(store)
	job := Job{"r", "main", "push", "", []Want{{Name: "MISSING", Required: false}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Inject(job); err != nil {
			b.Fatal(err)
		}
		if p := svc.probes(); p > 3 {
			b.Fatalf("probes=%d", p)
		}
	}
}
