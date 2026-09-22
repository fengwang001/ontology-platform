package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 第 11 条：并发写同键、并发开关快照、并发触发回收，-race 下干净。
func TestConcurrentMixed(t *testing.T) {
	s := newStore(t, Config{})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				tx, err := s.BeginTx()
				if err != nil {
					continue
				}
				_ = tx.Write("hot", []byte(fmt.Sprintf("w%d-%d", w, i)))
				_ = tx.Commit()
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap, err := s.Begin()
				if err != nil {
					continue
				}
				s.ReadAt(snap, "hot")
				s.Release(snap)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.Reclaim()
		}
	}()
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	if err := s.checkConsistency(); err != nil {
		t.Fatalf("并发后一致性: %v", err)
	}
}

// 第 11 条：一个键的长事务不得阻塞其他键的读写。
func TestLongTxDoesNotBlockOtherKeys(t *testing.T) {
	s := newStore(t, Config{})
	long, err := s.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	if err := long.Write("keyA", []byte("held")); err != nil {
		t.Fatal(err)
	}
	defer long.Rollback()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tx, err := s.BeginTx()
		if err != nil {
			t.Error(err)
			return
		}
		if err := tx.Write("keyB", []byte("ok")); err != nil {
			t.Error(err)
			return
		}
		if err := tx.Commit(); err != nil {
			t.Error(err)
			return
		}
		snap, err := s.Begin()
		if err != nil {
			t.Error(err)
			return
		}
		defer s.Release(snap)
		if got, lk := s.ReadAt(snap, "keyB"); lk != LookupFound || string(got) != "ok" {
			t.Errorf("keyB 应可读写，得到 %q/%v", got, lk)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("长事务持有期间其他键的读写被阻塞")
	}
}
