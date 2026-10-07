package compat

import (
	"reflect"
	"sync"
	"testing"
)

// TestConcurrentQueriesEquivalentToSerial 验证多个消费方并发查询同一份快照时，
// 结果与串行逐一查询完全等价，且查询不修改共享输入（配合 -race 运行）。
func TestConcurrentQueriesEquivalentToSerial(t *testing.T) {
	chain := fourChain()
	snap := snapAt(v4, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 5, "note": "n"}},
	})
	profiles := []Profile{
		readProfile(v1, fullRange()),
		{Name: "w", At: v2, Accepts: fullRange(), Mode: ModeWrite},
		{Name: "i", At: v1, Accepts: fullRange(), Mode: ModeRead,
			Independent: map[string]map[string]bool{"Order": {"qty": true}}},
		readProfile(v4, fullRange()),
		{Name: "oob", At: v1, Accepts: Range{Min: v1, Max: v2}, Mode: ModeRead},
	}

	// 串行基准。
	want := make([]ChainVerdict, len(profiles))
	for i, p := range profiles {
		want[i] = JudgePath(p, chain, snap)
	}

	// 并发重复查询。
	const goroutines = 32
	const iterations = 50
	errs := make(chan error, goroutines*iterations)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for it := 0; it < iterations; it++ {
				idx := (g + it) % len(profiles)
				got := JudgePath(profiles[idx], chain, snap)
				if !reflect.DeepEqual(got, want[idx]) {
					errs <- &mismatchError{idx: idx}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

type mismatchError struct{ idx int }

func (e *mismatchError) Error() string {
	return "concurrent result differs from serial result for profile " + string(rune('A'+e.idx))
}
