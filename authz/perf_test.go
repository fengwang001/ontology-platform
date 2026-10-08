package authz

import (
	"fmt"
	"testing"
)

// Performance proof: evaluation cost must not grow with the number of
// policies unrelated to the request (other namespaces). The store indexes
// policies by namespace, so the number of examined candidates depends only
// on the target namespace plus the root namespace. These tests assert the
// examined-candidate count directly, which is a deterministic proof, and a
// benchmark provides a wall-clock cross-check.
func buildIndexedStore(t *testing.T, relevant, root, unrelatedPerNS, unrelatedNS int) *Store {
	t.Helper()
	s := newStore(t)
	var set []Policy
	for i := 0; i < relevant; i++ {
		set = append(set, allowPolicy(fmt.Sprintf("rel-%d", i), "prod",
			Rule{Operation: &OperationSpec{Methods: []string{"POST"}}}))
	}
	for i := 0; i < root; i++ {
		set = append(set, auditPolicy(fmt.Sprintf("root-%d", i), testRoot,
			Rule{Operation: &OperationSpec{Methods: []string{"POST"}}}))
	}
	for ns := 0; ns < unrelatedNS; ns++ {
		for i := 0; i < unrelatedPerNS; i++ {
			set = append(set, denyPolicy(fmt.Sprintf("unrel-%d-%d", ns, i),
				fmt.Sprintf("ns-%d", ns), Rule{}))
		}
	}
	mustReplace(t, s, set...)
	return s
}

func TestEvaluationCostIndependentOfUnrelatedPolicies(t *testing.T) {
	req := baseReq()
	var baseline int
	for _, unrelated := range []int{0, 1000, 10000} {
		s := buildIndexedStore(t, 10, 5, 100, unrelated/100)
		_, examined, err := s.evaluate(req)
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		t.Logf("unrelated policies=%d -> examined candidates=%d", unrelated, examined)
		if unrelated == 0 {
			baseline = examined
			if baseline != 15 {
				t.Fatalf("expected exactly the 15 relevant+root candidates, got %d", baseline)
			}
			continue
		}
		if examined != baseline {
			t.Fatalf("examined candidates grew with unrelated policies: baseline=%d got=%d",
				baseline, examined)
		}
	}
}

func BenchmarkEvaluateWithManyUnrelatedPolicies(b *testing.B) {
	for _, unrelated := range []int{0, 10000} {
		b.Run(fmt.Sprintf("unrelated=%d", unrelated), func(b *testing.B) {
			s := buildIndexedStore(&testing.T{}, 10, 5, 100, unrelated/100)
			req := baseReq()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Evaluate(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
