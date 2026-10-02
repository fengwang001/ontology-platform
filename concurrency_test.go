package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentRevocationsSerialize(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 2, 64, 20)
	tokens := make([]*Token, 80)
	for index := range tokens {
		tokens[index] = testToken(fmt.Sprintf("parallel-%02d", index), "ops:read")
	}

	var wait sync.WaitGroup
	results := make(chan error, len(tokens))
	for _, token := range tokens {
		wait.Add(1)
		go func(token *Token) {
			defer wait.Done()
			_, err := service.Revoke(token, 0, 0)
			results <- err
		}(token)
	}
	wait.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if err.(*Error).Code != ErrLimit {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if successes != 20 || service.revocations.activeCount() != 20 {
		t.Fatalf("successes=%d records=%d", successes, service.revocations.activeCount())
	}
}
