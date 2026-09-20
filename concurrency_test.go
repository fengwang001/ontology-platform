package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 并发插入规范化后相同的键：恰好一个成功，
// 其余全部得到指向成功者的冲突错误，索引保持一致。
func TestConcurrentInsertExactlyOneSucceeds(t *testing.T) {
	c := NewChecker(Normalizer{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uniq_name", Props: []string{"name"}})

	variants := []string{"alice", "ALICE", " Alice ", "aLiCe", "  ALICE",
		"Alice", "ALICE ", " alICE", "aLICE  ", " AlicE "}
	const goroutines = 64

	var wg sync.WaitGroup
	results := make([]error, goroutines)
	pks := make([]string, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pks[i] = fmt.Sprintf("pk-%d", i)
			results[i] = c.Add(Record{
				PK:     pks[i],
				Values: map[string]*string{"name": Str(variants[i%len(variants)])},
			})
		}(i)
	}
	wg.Wait()

	var winner string
	successes := 0
	for i, err := range results {
		if err == nil {
			successes++
			winner = pks[i]
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	for i, err := range results {
		if err == nil {
			continue
		}
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("goroutine %d: error type = %T, want *ConflictError", i, err)
		}
		if ce.ExistingPK != winner {
			t.Errorf("goroutine %d: ExistingPK = %q, want winner %q", i, ce.ExistingPK, winner)
		}
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 并发混合增删后索引仍然一致。
func TestConcurrentMixedOpsSelfCheck(t *testing.T) {
	c := NewChecker(Normalizer{CaseFold: true},
		Constraint{Name: "uniq_name", Props: []string{"name"}})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pk := fmt.Sprintf("pk-%d", i)
			name := fmt.Sprintf("user-%d", i%8)
			_ = c.Add(Record{PK: pk, Values: map[string]*string{"name": Str(name)}})
			if i%2 == 0 {
				c.Remove(pk)
			}
		}(i)
	}
	wg.Wait()
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// SelfCheck 在空检查器上直接可用。
func TestSelfCheckEmpty(t *testing.T) {
	c := newChecker(Normalizer{})
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck on empty checker: %v", err)
	}
}
