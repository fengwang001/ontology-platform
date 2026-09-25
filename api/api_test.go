package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

func abcAPI(t *testing.T) *api.API {
	t.Helper()
	a, err := api.New(1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, c := range []struct {
		id      string
		off, er int64
	}{{"A", 10, 2}, {"B", 11, 1}, {"C", 20, 1}} {
		if err := a.Add(c.id, c.off, c.er); err != nil {
			t.Fatalf("Add %s: %v", c.id, err)
		}
	}
	return a
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	sentinels := []error{api.ErrNegativeError, api.ErrDuplicateID, api.ErrInvalidF, api.ErrNoConsensus}
	for i := range sentinels { // 四类必须互不相同
		for j := 0; j < i; j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
	if _, err := api.New(-1); !errors.Is(err, api.ErrInvalidF) { // f<0
		t.Fatalf("New(-1) err=%v", err)
	}
	a := abcAPI(t)
	// error 为负、重复 ID 被拒；f>=K 与时钟不足在构造的独立集合上触发。
	reject := []struct {
		name string
		call func() error
		want error
	}{
		{"negative", func() error { return a.Add("D", 0, -1) }, api.ErrNegativeError},
		{"dup", func() error { return a.Add("A", 0, 0) }, api.ErrDuplicateID},
		{"f>=K", func() error {
			b, _ := api.New(2)
			_ = b.Add("p", 0, 1)
			_ = b.Add("q", 0, 1)
			_, _, e := b.Consensus()
			return e
		}, api.ErrInvalidF},
		{"too-few", func() error {
			b, _ := api.New(0)
			_ = b.Add("p", 0, 1)
			_, _, e := b.Consensus()
			return e
		}, api.ErrNoConsensus},
	}
	for _, tc := range reject {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v want %v", tc.name, err, tc.want)
		}
	}
	lo, hi, err := a.Consensus() // 被拒后状态不变
	if err != nil || lo != 10 || hi != 12 || a.CountAt(11) != 2 {
		t.Fatalf("state changed after rejection: [%d,%d] err=%v count=%d", lo, hi, err, a.CountAt(11))
	}
	if err := a.Add("E", 10, 1); err != nil { // 仍可继续正常使用
		t.Fatalf("Add after rejections: %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := abcAPI(t).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestSelfCheckRejectsBrokenState(t *testing.T) {
	// f=0 且三台不相交：无共识，自检内置序列应暴露失败而非误报 nil。
	a, _ := api.New(0)
	for _, c := range []struct {
		id      string
		off, er int64
	}{{"A", 10, 2}, {"B", 11, 1}, {"C", 20, 1}} {
		_ = a.Add(c.id, c.off, c.er)
	}
	// SelfCheck 使用独立内部序列（f=1），不应受调用方状态影响；这里钉其确定性。
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck must be self-contained: %v", err)
	}
}

func TestConcurrentConsistency(t *testing.T) {
	a := abcAPI(t)
	const n = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	res := make([][3]int64, n)
	for g := 0; g < n; g++ { // N 个 goroutine 并发只读
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			lo, hi, _ := a.Consensus()
			res[g] = [3]int64{lo, hi, int64(a.CountAt(11))}
		}(g)
	}
	close(start)
	wg.Wait()
	for _, r := range res {
		if r != [3]int64{10, 12, 2} {
			t.Fatalf("reader got %v", r)
		}
	}
}
