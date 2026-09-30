package ontology

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentReservationsConserveBalance(t *testing.T) {
	const workers = 200
	bank := NewBank()
	if err := bank.CreateAccount("a", 50, 0, 100, workers); err != nil {
		t.Fatalf("CreateAccount(a): %v", err)
	}
	if err := bank.CreateAccount("b", 50, 0, 100, workers); err != nil {
		t.Fatalf("CreateAccount(b): %v", err)
	}

	var acceptedA atomic.Int64
	var acceptedB atomic.Int64
	var stopReaders atomic.Bool
	var reserveWG sync.WaitGroup
	var readerWG sync.WaitGroup

	for id := int64(1); id <= workers; id++ {
		if err := bank.BeginTransaction(id); err != nil {
			t.Fatalf("BeginTransaction(%d): %v", id, err)
		}
	}

	for reader := 0; reader < 4; reader++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for !stopReaders.Load() {
				for _, id := range []string{"a", "b"} {
					result, err := bank.Read(id)
					if err != nil {
						t.Errorf("Read(%s): %v", id, err)
						return
					}
					lower := result.Balance
					upper := result.Balance
					if !result.Certain {
						lower = result.Lower
						upper = result.Upper
					}
					if lower < 0 || upper > 100 {
						t.Errorf("account %s range [%d,%d] out of bounds", id, lower, upper)
						return
					}
				}
			}
		}()
	}

	for id := int64(1); id <= workers; id++ {
		reserveWG.Add(1)
		go func(id int64) {
			defer reserveWG.Done()
			delta := int64(1)
			if id%2 == 0 {
				delta = -1
			}

			if id%3 == 0 {
				if err := bank.Reserve(id, "a", delta); err != nil {
					return
				}
				if err := bank.Reserve(id, "b", delta); err != nil {
					if abortErr := bank.Abort(id); abortErr != nil {
						t.Errorf("Abort(%d): %v", id, abortErr)
					}
					return
				}
				acceptedA.Add(delta)
				acceptedB.Add(delta)
				if err := bank.Commit(id); err != nil {
					t.Errorf("Commit(%d): %v", id, err)
				}
				return
			}

			if err := bank.Reserve(id, "a", delta); err != nil {
				return
			}
			acceptedA.Add(delta)
			if err := bank.Commit(id); err != nil {
				t.Errorf("Commit(%d): %v", id, err)
			}
		}(id)
	}

	reserveWG.Wait()
	stopReaders.Store(true)
	readerWG.Wait()

	a, err := bank.Read("a")
	if err != nil || !a.Certain {
		t.Fatalf("Read(a) = %+v, %v", a, err)
	}
	b, err := bank.Read("b")
	if err != nil || !b.Certain {
		t.Fatalf("Read(b) = %+v, %v", b, err)
	}
	if want := int64(50) + acceptedA.Load(); a.Balance != want {
		t.Fatalf("a final = %d, want %d", a.Balance, want)
	}
	if want := int64(50) + acceptedB.Load(); b.Balance != want {
		t.Fatalf("b final = %d, want %d", b.Balance, want)
	}
	t.Logf("并发后 a=%d（接受增量=%d），b=%d（接受增量=%d）", a.Balance, acceptedA.Load(), b.Balance, acceptedB.Load())
}

func TestConcurrentReservationAndFinishSameTransaction(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		bank := NewBank()
		if err := bank.CreateAccount("cash", 50, 0, 100, 20); err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		if err := bank.BeginTransaction(1); err != nil {
			t.Fatalf("BeginTransaction: %v", err)
		}

		var workers sync.WaitGroup
		for i := 0; i < 8; i++ {
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				delta := int64(1)
				if i%2 == 0 {
					delta = -1
				}
				_ = bank.Reserve(1, "cash", delta)
			}(i)
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := bank.Commit(1); err != nil && !errors.Is(err, ErrNoReservations) {
				t.Errorf("Commit: %v", err)
			}
		}()
		workers.Wait()
		if err := bank.Commit(1); err != nil && !errors.Is(err, ErrInvalidTransaction) && !errors.Is(err, ErrNoReservations) {
			t.Fatalf("cleanup Commit: %v", err)
		}

		result, err := bank.Read("cash")
		if err != nil || !result.Certain {
			t.Fatalf("iteration %d final read = %+v, %v", iteration, result, err)
		}
		if result.Balance < 0 || result.Balance > 100 {
			t.Fatalf("iteration %d balance = %d out of bounds", iteration, result.Balance)
		}
	}
}

func TestAllSubsetsAndCommitOrders(t *testing.T) {
	deltas := []int64{-1, -2, -3, 1, 2, 3}
	scenarios := 0

	for commitMask := 0; commitMask < 1<<len(deltas); commitMask++ {
		committed := make([]int, 0, len(deltas))
		expected := int64(10)
		for index, delta := range deltas {
			if commitMask&(1<<index) != 0 {
				committed = append(committed, index)
				expected += delta
			}
		}

		var enumerate func(int)
		used := make([]bool, len(committed))
		order := make([]int, 0, len(committed))
		enumerate = func(depth int) {
			if depth == len(committed) {
				scenarios++
				finalBalance := replaySubsetOrder(t, deltas, order)
				if finalBalance != expected {
					t.Fatalf("mask=%06b order=%v final=%d, want %d", commitMask, order, finalBalance, expected)
				}
				return
			}

			for i, index := range committed {
				if used[i] {
					continue
				}
				used[i] = true
				order = append(order, index)
				enumerate(depth + 1)
				order = order[:len(order)-1]
				used[i] = false
			}
		}
		enumerate(0)
	}

	t.Logf("已穷举 %d 个提交子集/顺序组合，并对每个提交前缀检查余额界内", scenarios)
}

func replaySubsetOrder(t *testing.T, deltas []int64, order []int) int64 {
	t.Helper()
	bank := NewBank()
	if err := bank.CreateAccount("cash", 10, 0, 20, int64(len(deltas))); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	committed := make(map[int]bool, len(order))
	for index, delta := range deltas {
		if err := bank.BeginTransaction(int64(index + 1)); err != nil {
			t.Fatalf("BeginTransaction(%d): %v", index+1, err)
		}
		if err := bank.Reserve(int64(index+1), "cash", delta); err != nil {
			t.Fatalf("Reserve(%d): %v", delta, err)
		}
	}

	for position, index := range order {
		committed[index] = true
		if err := bank.Commit(int64(index + 1)); err != nil {
			t.Fatalf("Commit position %d txn %d: %v", position, index+1, err)
		}
		result, err := bank.Read("cash")
		if err != nil {
			t.Fatalf("Read after prefix %v error = %v", order[:position+1], err)
		}
		if result.Lower < 0 || result.Upper > 20 {
			t.Fatalf("prefix %v range [%d,%d] out of bounds", order[:position+1], result.Lower, result.Upper)
		}
	}

	for index := range deltas {
		if !committed[index] {
			if err := bank.Abort(int64(index + 1)); err != nil {
				t.Fatalf("Abort(%d): %v", index+1, err)
			}
		}
	}

	result, err := bank.Read("cash")
	if err != nil || !result.Certain {
		t.Fatalf("final Read = %+v, %v", result, err)
	}
	return result.Balance
}
