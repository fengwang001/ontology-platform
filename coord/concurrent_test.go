package coord

import (
	"sync"
	"testing"
)

// 多个互不相同的事务并发 Run/Execute：串行化后账目恒等式成立。
func TestConcurrentTransactions(t *testing.T) {
	co, _, lg, _ := setup(t, 100, 10000, map[string]int64{"a": 100000, "b": 100000})
	const n = 40
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			xid := string(rune('A'+i/26)) + string(rune('a'+i%26))
			brs := []BranchSpec{
				{BR: "1", Acct: "a", Amount: 10},
				{BR: "2", Acct: "b", Amount: 10},
			}
			if err := co.Begin(xid, brs, 0); err != nil {
				errCh <- err
				return
			}
			d, err := co.Run(xid, 0)
			if err != nil {
				errCh <- err
				return
			}
			if _, err := co.Execute(xid, 0); err != nil {
				errCh <- err
				return
			}
			if d != Commit {
				errCh <- errUnexpectedD
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if lg.Fz("a") != 0 || lg.Fz("b") != 0 {
		t.Fatalf("fz a=%d b=%d want 0", lg.Fz("a"), lg.Fz("b"))
	}
	if lg.Bal("a") != 100000-int64(n)*10 || lg.Bal("b") != 100000-int64(n)*10 {
		t.Fatalf("bal a=%d b=%d", lg.Bal("a"), lg.Bal("b"))
	}
}

var errUnexpectedD = &stringError{"unexpected decision"}

type stringError struct{ s string }

func (e *stringError) Error() string { return e.s }
