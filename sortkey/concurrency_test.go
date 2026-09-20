package sortkey

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// TestConcurrentInsertSameGap 多个 goroutine 同时在同一对邻居之间
// 插入：全部成功、键无重复、最终顺序由元素内容的字典序决定，
// 与 goroutine 调度顺序无关。
func TestConcurrentInsertSameGap(t *testing.T) {
	const goroutines = 64
	run := func() []Entry {
		s := NewSequence(NewFractional(64))
		kL, err := s.Insert("", "", "L")
		if err != nil {
			t.Fatal(err)
		}
		kR, err := s.Insert(kL, "", "R")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := s.Insert(kL, kR, fmt.Sprintf("v%03d", i))
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent insert: %v", err)
			}
		}
		if err := s.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
		return s.Snapshot()
	}

	first := run()
	// 最终顺序确定：间隙内的值按字典序排列，边界 L、R 不动。
	got := values(first)
	want := []string{"L"}
	for i := 0; i < goroutines; i++ {
		want = append(want, fmt.Sprintf("v%03d", i))
	}
	sort.Strings(want[1:])
	want = append(want, "R")
	if !equalStrings(got, want) {
		t.Fatalf("final order not sorted by value:\ngot  %v\nwant %v", got, want)
	}
	// 重复运行，结果与调度无关。
	for trial := 0; trial < 5; trial++ {
		if again := values(run()); !equalStrings(again, got) {
			t.Fatalf("trial %d: order differs across runs", trial)
		}
	}
}

// TestRebalanceAtomicUnderConcurrentRead 重排期间并发读取整个序列，
// 每次读到的键必须全部来自重排前或全部来自重排后，不允许混合；
// 且任何快照中元素的相对顺序一致、键严格递增。
func TestRebalanceAtomicUnderConcurrentRead(t *testing.T) {
	const n = 50
	gen := NewFractional(64)
	s := NewSequence(gen)
	for i := 0; i < n; i++ {
		if _, err := s.Insert("", "", fmt.Sprintf("e%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	oldKeys := make(map[string]bool, n)
	for _, e := range s.Snapshot() {
		oldKeys[e.Key] = true
	}
	newKeyList, err := gen.EvenKeys(n)
	if err != nil {
		t.Fatal(err)
	}
	newKeys := make(map[string]bool, n)
	for _, k := range newKeyList {
		newKeys[k] = true
	}

	stop := make(chan struct{})
	errs := make(chan error, 128)
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				old, fresh := 0, 0
				for i, e := range snap {
					switch {
					case oldKeys[e.Key]:
						old++
					case newKeys[e.Key]:
						fresh++
					default:
						errs <- fmt.Errorf("snapshot contains unknown key %q", e.Key)
						return
					}
					if i > 0 && snap[i-1].Key >= e.Key {
						errs <- fmt.Errorf("snapshot keys not strictly increasing at %d", i)
						return
					}
					if i > 0 && snap[i-1].Value >= e.Value {
						errs <- fmt.Errorf("snapshot order changed at %d", i)
						return
					}
				}
				if old > 0 && fresh > 0 {
					errs <- fmt.Errorf("mixed snapshot: %d old keys, %d new keys", old, fresh)
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if err := s.Rebalance(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// 重排后顺序不变、自检通过。
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	got := values(s.Snapshot())
	for i, v := range got {
		if v != fmt.Sprintf("e%03d", i) {
			t.Fatalf("order changed after rebalances: %v", got)
		}
	}
}
