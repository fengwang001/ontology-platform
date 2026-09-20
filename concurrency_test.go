package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 并发插入规范化后相同键的记录：恰好一个成功，其余全部冲突，
// 且每个冲突错误都指向那个成功者；索引自检必须通过。
func TestConcurrentInsertsExactlyOneSucceeds(t *testing.T) {
	c := NewChecker(
		NormOptions{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uniq_name", Columns: []string{"name"}},
	)
	const n = 32
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = c.Insert(fmt.Sprintf("pk-%d", i),
				map[string]Value{"name": String("  Alice  ")})
		}(i)
	}
	wg.Wait()

	var winner string
	wins := 0
	for i, err := range results {
		if err == nil {
			wins++
			winner = fmt.Sprintf("pk-%d", i)
			continue
		}
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("loser got %T, want *ConflictError", err)
		}
		if ce.ExistingPK == "" {
			t.Fatal("conflict error must name the winning record")
		}
	}
	if wins != 1 {
		t.Fatalf("got %d winners, want exactly 1", wins)
	}
	for i, err := range results {
		if err == nil {
			continue
		}
		ce := err.(*ConflictError)
		if ce.ExistingPK != winner {
			t.Fatalf("loser %d points at %q, want winner %q", i, ce.ExistingPK, winner)
		}
	}
	if err := c.CheckInvariants(); err != nil {
		t.Fatalf("index invariant violated: %v", err)
	}
}
