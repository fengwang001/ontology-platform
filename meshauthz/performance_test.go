package meshauthz

import (
	"fmt"
	"testing"
)

// buildPerfSet creates a policy set with `relevant` policies split
// between the target namespace and the root namespace, plus
// `irrelevant` policies spread across many other namespaces.
func buildPerfSet(relevant, irrelevant int) []Policy {
	policies := make([]Policy, 0, relevant+irrelevant)
	actions := []Action{ActionAllow, ActionDeny, ActionAudit}
	for i := 0; i < relevant; i++ {
		ns := "prod"
		if i%2 == 0 {
			ns = testRootNS
		}
		policies = append(policies, Policy{
			Name:      fmt.Sprintf("relevant-%d", i),
			Namespace: ns,
			Selector:  map[string]string{"app": "api"},
			Action:    actions[i%3],
			Rules:     []Rule{{Operation: &Operation{Methods: []string{"GET"}}}},
		})
	}
	for i := 0; i < irrelevant; i++ {
		policies = append(policies, Policy{
			Name:      fmt.Sprintf("irrelevant-%d", i),
			Namespace: fmt.Sprintf("tenant-%d", i%97),
			Action:    actions[i%3],
			Rules:     []Rule{{}},
		})
	}
	return policies
}

// TestEvaluationCostIndependentOfIrrelevantPolicies proves the
// evaluation complexity claim in a directly checkable way: the number
// of policies an evaluation inspects (exposed via ExaminedPolicies)
// stays exactly constant as the number of policies in unrelated
// namespaces grows from zero to ten thousand.
func TestEvaluationCostIndependentOfIrrelevantPolicies(t *testing.T) {
	const relevant = 30
	examinedFor := func(irrelevant int) int64 {
		s := newTestStore(t)
		mustReplace(t, s, buildPerfSet(relevant, irrelevant))
		before := s.ExaminedPolicies()
		res := mustEvaluate(t, s, baseRequest())
		got := s.ExaminedPolicies() - before
		t.Logf("irrelevant=%d decision=%s examined-policies=%d", irrelevant, res.Decision, got)
		return got
	}

	base := examinedFor(0)
	for _, irrelevant := range []int{100, 1000, 10000} {
		if got := examinedFor(irrelevant); got != base {
			t.Fatalf("examined policies grew with irrelevant policies: base=%d irrelevant=%d got=%d",
				base, irrelevant, got)
		}
	}
	if base != relevant {
		t.Fatalf("examined = %d, want exactly the %d relevant (target+root ns) policies", base, relevant)
	}
}

// BenchmarkEvaluateScalesFlat reports evaluation latency as the number
// of unrelated-namespace policies grows; the timings should stay flat.
// Run with: go test -bench=. -benchtime=1000x ./meshauthz/
func BenchmarkEvaluateScalesFlat(b *testing.B) {
	for _, irrelevant := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprintf("irrelevant=%d", irrelevant), func(b *testing.B) {
			s, err := NewStore(testRootNS)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := s.ReplaceAll(buildPerfSet(30, irrelevant)); err != nil {
				b.Fatal(err)
			}
			req := baseRequest()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Evaluate(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
