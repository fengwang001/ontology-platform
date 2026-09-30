package stm_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/stm"
)

// TestTransferInvariant runs many concurrent transfer transactions between
// accounts whose balances sum to a constant. Every execution of every
// transaction body (including executions later discarded and re-run) must
// observe the constant sum, and the final sum must equal it too.
func TestTransferInvariant(t *testing.T) {
	const (
		accounts   = 8
		perAccount = 1000
		workers    = 16
		iters      = 200
	)
	const total = accounts * perAccount
	t.Logf("input: %d accounts x %d, total=%d; %d workers x %d random transfers", accounts, perAccount, total, workers, iters)
	t.Logf("basis: every body execution (even re-run ones) sees sum == %d; final sum == %d", total, total)

	s := stm.New()
	vars := make([]*stm.TVar, accounts)
	for i := range vars {
		vars[i] = s.NewTVar(perAccount)
	}

	var violations atomic.Int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < iters; i++ {
				from, to := rng.Intn(accounts), rng.Intn(accounts)
				if from == to {
					continue
				}
				amount := rng.Intn(100)
				err := s.Atomically(func(tx *stm.Txn) error {
					sum := 0
					for _, v := range vars {
						sum += tx.Get(v)
					}
					if sum != total {
						violations.Add(1)
					}
					tx.Set(vars[from], tx.Get(vars[from])-amount)
					tx.Set(vars[to], tx.Get(vars[to])+amount)
					return nil
				})
				if err != nil {
					t.Errorf("transfer failed: %v", err)
				}
			}
		}(int64(w))
	}
	wg.Wait()

	finalSum := 0
	if err := s.Atomically(func(tx *stm.Txn) error {
		for _, v := range vars {
			finalSum += tx.Get(v)
		}
		return nil
	}); err != nil {
		t.Fatalf("final read failed: %v", err)
	}

	t.Logf("output: invariant violations=%d, final sum=%d", violations.Load(), finalSum)
	if violations.Load() != 0 {
		t.Fatalf("observed %d inconsistent snapshots", violations.Load())
	}
	if finalSum != total {
		t.Fatalf("final sum = %d, want %d", finalSum, total)
	}
}

// TestProducerConsumerRetry: a consumer retries (blocks) on an empty box
// until a producer writes it; the wakeup must not be lost.
func TestProducerConsumerRetry(t *testing.T) {
	s := stm.New()
	box := s.NewTVar(0)
	t.Logf("input: box=0; consumer retries while box==0, producer writes 42 after 50ms")
	t.Logf("basis: blocked consumer must wake once box is committed and observe 42")

	got := make(chan int, 1)
	go func() {
		_ = s.Atomically(func(tx *stm.Txn) error {
			v := tx.Get(box)
			if v == 0 {
				return tx.Retry()
			}
			got <- v
			return nil
		})
	}()

	time.Sleep(50 * time.Millisecond)
	if err := s.Atomically(func(tx *stm.Txn) error {
		tx.Set(box, 42)
		return nil
	}); err != nil {
		t.Fatalf("producer failed: %v", err)
	}

	select {
	case v := <-got:
		t.Logf("output: consumer got %d", v)
		if v != 42 {
			t.Fatalf("consumer got %d, want 42", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer was not woken (lost wakeup)")
	}
}

// TestOrElseRightBranch: when the left branch retries, the right branch runs
// and its writes commit.
func TestOrElseRightBranch(t *testing.T) {
	s := stm.New()
	v := s.NewTVar(10)
	t.Logf("input: v=10; left retries, right sets v=20")
	t.Logf("basis: left retry discards left and selects right; right's write commits")

	var rightRan bool
	err := s.Atomically(func(tx *stm.Txn) error {
		return tx.OrElse(
			func(tx *stm.Txn) error { return tx.Retry() },
			func(tx *stm.Txn) error {
				rightRan = true
				tx.Set(v, 20)
				return nil
			},
		)
	})
	if err != nil {
		t.Fatalf("atomically failed: %v", err)
	}

	final := 0
	_ = s.Atomically(func(tx *stm.Txn) error {
		final = tx.Get(v)
		return nil
	})
	t.Logf("output: rightRan=%v, v=%d", rightRan, final)
	if !rightRan {
		t.Fatal("right branch was not selected")
	}
	if final != 20 {
		t.Fatalf("v = %d, want 20", final)
	}
}

// TestOrElseLeftWritesUndoneReadsWake: the left branch writes w and reads r,
// then retries; both branches retry, so the transaction blocks. The left
// write must never become visible, and a commit to r (read only by the left
// branch) must wake the transaction.
func TestOrElseLeftWritesUndoneReadsWake(t *testing.T) {
	s := stm.New()
	w := s.NewTVar(0)      // written only by the left branch
	r := s.NewTVar(0)      // read only by the left branch
	marker := s.NewTVar(0) // written by the right branch on success
	t.Logf("input: w=0 r=0 marker=0; left writes w=99, reads r, retries; right retries while r==0 else sets marker=7")
	t.Logf("basis: left's write to w is undone; left's read of r stays in the read set and a write to r wakes the transaction")

	var firstAttempt sync.Once
	attempted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Atomically(func(tx *stm.Txn) error {
			return tx.OrElse(
				func(tx *stm.Txn) error {
					tx.Set(w, 99)
					tx.Get(r)
					firstAttempt.Do(func() { close(attempted) })
					return tx.Retry()
				},
				func(tx *stm.Txn) error {
					if tx.Get(r) == 0 {
						return tx.Retry()
					}
					tx.Set(marker, 7)
					return nil
				},
			)
		})
	}()

	<-attempted
	time.Sleep(50 * time.Millisecond) // let the transaction block on its read set

	// While blocked, the left branch's write must not be visible.
	seenW := -1
	_ = s.Atomically(func(tx *stm.Txn) error {
		seenW = tx.Get(w)
		return nil
	})
	t.Logf("output: while blocked, w=%d (left write must be invisible)", seenW)
	if seenW != 0 {
		t.Fatalf("left branch write leaked: w = %d, want 0", seenW)
	}

	// Writing r (read only by the left branch) must wake the transaction.
	if err := s.Atomically(func(tx *stm.Txn) error {
		tx.Set(r, 1)
		return nil
	}); err != nil {
		t.Fatalf("wakeup write failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("transaction failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("transaction was not woken by a write to a left-branch read variable")
	}

	finalW, finalMarker := -1, -1
	_ = s.Atomically(func(tx *stm.Txn) error {
		finalW = tx.Get(w)
		finalMarker = tx.Get(marker)
		return nil
	})
	t.Logf("output: after wakeup, w=%d marker=%d", finalW, finalMarker)
	if finalW != 0 {
		t.Fatalf("left branch write committed: w = %d, want 0", finalW)
	}
	if finalMarker != 7 {
		t.Fatalf("right branch did not run after wakeup: marker = %d, want 7", finalMarker)
	}
}

// TestRetryEmptyReadSet: retrying without having read anything can never be
// woken, so the transaction aborts with ErrBlockedForever.
func TestRetryEmptyReadSet(t *testing.T) {
	s := stm.New()
	t.Logf("input: body retries without reading any variable")
	t.Logf("basis: empty read set can never be written, so retry must abort with ErrBlockedForever")

	err := s.Atomically(func(tx *stm.Txn) error {
		return tx.Retry()
	})
	t.Logf("output: err=%v", err)
	if !errors.Is(err, stm.ErrBlockedForever) {
		t.Fatalf("err = %v, want ErrBlockedForever", err)
	}
}

// TestTooManyVars: accessing more than 64 distinct variables in one
// execution aborts with ErrTooManyVars and no writes take effect.
func TestTooManyVars(t *testing.T) {
	s := stm.New()
	vars := make([]*stm.TVar, 65)
	for i := range vars {
		vars[i] = s.NewTVar(0)
	}
	t.Logf("input: 65 variables; body writes vars[0]=999 then reads all 65")
	t.Logf("basis: the 65th distinct variable exceeds the limit; abort with ErrTooManyVars and no writes")

	err := s.Atomically(func(tx *stm.Txn) error {
		tx.Set(vars[0], 999)
		for _, v := range vars {
			tx.Get(v)
		}
		return nil
	})
	t.Logf("output: err=%v", err)
	if !errors.Is(err, stm.ErrTooManyVars) {
		t.Fatalf("err = %v, want ErrTooManyVars", err)
	}

	final := -1
	_ = s.Atomically(func(tx *stm.Txn) error {
		final = tx.Get(vars[0])
		return nil
	})
	if final != 0 {
		t.Fatalf("aborted transaction leaked a write: vars[0] = %d, want 0", final)
	}
}

// TestMisuse covers the remaining misuse rules and error/panic propagation.
func TestMisuse(t *testing.T) {
	s := stm.New()
	v := s.NewTVar(1)

	// Nested transaction: the whole transaction aborts with ErrNested.
	err := s.Atomically(func(tx *stm.Txn) error {
		tx.Set(v, 100)
		return s.Atomically(func(tx *stm.Txn) error { return nil })
	})
	t.Logf("nested: err=%v", err)
	if !errors.Is(err, stm.ErrNested) {
		t.Fatalf("nested err = %v, want ErrNested", err)
	}

	// Foreign TVar from another STM instance.
	other := stm.New().NewTVar(0)
	err = s.Atomically(func(tx *stm.Txn) error {
		tx.Get(other)
		return nil
	})
	t.Logf("foreign: err=%v", err)
	if !errors.Is(err, stm.ErrForeignTVar) {
		t.Fatalf("foreign err = %v, want ErrForeignTVar", err)
	}

	// Handle used after the transaction ended.
	var stash *stm.Txn
	_ = s.Atomically(func(tx *stm.Txn) error {
		stash = tx
		return nil
	})
	func() {
		defer func() {
			r := recover()
			t.Logf("closed handle: panic=%v", r)
			if r != stm.ErrTxnClosed {
				t.Fatalf("closed handle panic = %v, want ErrTxnClosed", r)
			}
		}()
		stash.Get(v)
	}()

	// Body error aborts and propagates unchanged; writes do not take effect.
	sentinel := errors.New("boom")
	err = s.Atomically(func(tx *stm.Txn) error {
		tx.Set(v, 100)
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("body err = %v, want the sentinel", err)
	}

	// Body panic aborts and re-panics unchanged.
	func() {
		defer func() {
			if r := recover(); r != "kaboom" {
				t.Fatalf("panic = %v, want kaboom", r)
			}
		}()
		_ = s.Atomically(func(tx *stm.Txn) error {
			tx.Set(v, 100)
			panic("kaboom")
		})
	}()

	final := -1
	_ = s.Atomically(func(tx *stm.Txn) error {
		final = tx.Get(v)
		return nil
	})
	t.Logf("output: after aborted transactions, v=%d", final)
	if final != 1 {
		t.Fatalf("aborted writes leaked: v = %d, want 1", final)
	}
}
