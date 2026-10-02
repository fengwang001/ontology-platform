package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentOperationsUseConsistentSnapshots(t *testing.T) {
	v, _ := New(16, 10000)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "ca", subject: "ca", issuer: "root", key: "kc", authKey: "kr", nb: 0, na: 1_000_000, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "leaf", subject: "leaf", issuer: "ca", key: "kl", authKey: "kc", nb: 0, na: 1_000_000, san: []string{"a.example.com"}})

	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for step := 0; step < 100; step++ {
				id := certSpec{
					id:      "parallel-" + string(rune('a'+worker)) + "-" + string(rune('0'+step/10)) + string(rune('0'+step%10)),
					subject: "parallel", issuer: "root", key: "kp", authKey: "kr",
					nb: 0, na: 1_000_000, ca: true, pathLen: -1,
				}
				_ = v.Add(testCert(id))
				_ = v.Trust(Bytes("leaf"))
				_ = v.Revoke(Bytes("ca"), 1_000_001)
				result, err := v.Verify(Bytes("leaf"), "a.example.com", 10)
				if err != nil {
					t.Errorf("concurrent verify: %v", err)
					return
				}
				if result.NoPath && result.Failure != nil {
					t.Errorf("result cannot be both no-path and validation failure: %#v", result)
					return
				}
			}
		}(worker)
	}
	wait.Wait()
}
