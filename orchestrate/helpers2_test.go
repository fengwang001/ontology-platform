package orchestrate

import "ontology/policy"

func testRetryAllPolicy(n int) policy.Policy {
	return policy.Policy{
		MaxRetries:    n,
		IsRetryable:   func(error) bool { return true },
		BackoffMillis: []int64{1, 1},
	}
}
