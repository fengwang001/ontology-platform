package tcc

import (
	"fmt"
	"testing"

	"ontology/ledger"
)

// 一次操作考察堆项数 ≤ 到期数 + 1；无关分支分 1 与 10000 两档。
func TestExpiryPeekBudget(t *testing.T) {
	for _, n := range []int{1, 10_000} {
		lg := ledger.New()
		for i := 0; i < n; i++ {
			must(t, lg.Deposit(fmt.Sprintf("acct%d", i), 1_000))
		}
		rm, err := New(lg, 100_000, int64(n)+5)
		must(t, err)
		for i := 0; i < n; i++ {
			must(t, rm.Try("x", fmt.Sprintf("b%d", i), fmt.Sprintf("acct%d", i),
				1, int64(i))) // deadline = i + 100000，逐条错开
		}
		// 无到期：只考察堆顶 1 项。
		if _, err := rm.Avail("acct0", int64(n)); err != nil {
			t.Fatal(err)
		}
		if got := rm.ExpiryPeeks(); got != 1 {
			t.Fatalf("n=%d no-expiry peeks=%d want 1", n, got)
		}
		// k 个到期：考察 k+1 项（第 k+1 项未到期即停）。
		k := n / 3
		if k == 0 {
			k = 1
		}
		if _, err := rm.Avail("acct0", int64(100_000+k-1)); err != nil {
			t.Fatal(err)
		}
		want := k + 1
		if k >= n {
			want = n
		}
		if got := rm.ExpiryPeeks(); got != want {
			t.Fatalf("n=%d k=%d peeks=%d want %d", n, k, got, want)
		}
		// 全部到期：堆空为止，考察 n 项（不存在“多 1”项）。
		if _, err := rm.Avail("acct0", 200_000); err != nil {
			t.Fatal(err)
		}
		if got := rm.ExpiryPeeks(); got != n {
			t.Fatalf("n=%d all-expired peeks=%d want %d", n, got, n)
		}
	}
}
