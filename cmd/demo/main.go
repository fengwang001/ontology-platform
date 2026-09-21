// Command demo checks each batcher semantic and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/batcher"
)

type sink struct {
	batches []batcher.Batch
	fail    bool
	hook    func()
}

func (s *sink) Deliver(b batcher.Batch) error {
	if s.hook != nil {
		s.hook()
	}
	s.batches = append(s.batches, b)
	if s.fail {
		return errors.New("boom")
	}
	return nil
}

func ok(name string, cond bool) {
	mark := "OK  "
	if !cond {
		mark = "FAIL"
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	now := time.Now()
	// 1. triple trigger: count, bytes, age.
	s1 := &sink{}
	b1 := batcher.New(s1, 2, 4, time.Second, func() time.Time { return now })
	_ = b1.Add("a")
	_ = b1.Add("b") // count trigger
	_ = b1.Add("cccc")
	_ = b1.Add("d") // byte trigger (4+1 >= 4)
	now = now.Add(2 * time.Second)
	_ = b1.Add("z")
	b1.Tick() // age trigger
	ok("1 triple trigger (count/bytes/age)", len(s1.batches) == 3)

	// 2. oversized item rejected without side effects.
	s2 := &sink{}
	b2 := batcher.New(s2, 10, 3, time.Hour, nil)
	err := b2.Add("toolong")
	n2, by2 := b2.Pending()
	ok("2 ErrTooLarge, item not buffered", err == batcher.ErrTooLarge && n2 == 0 && by2 == 0 && len(s2.batches) == 0)

	// 3+4. seq strict incl. failures; failed batch preserved.
	s3 := &sink{fail: true}
	b3 := batcher.New(s3, 2, 1<<20, time.Hour, nil)
	_ = b3.Add("x")
	_ = b3.Add("y")
	s3.fail = false
	_ = b3.Add("p")
	_ = b3.Add("q")
	f3 := b3.Failed()
	pn3, _ := b3.Pending()
	ok("3 seq strict, failures occupy seq", s3.batches[0].Seq == 1 && s3.batches[1].Seq == 2)
	ok("4 failed batch preserved, no reflow", len(f3) == 1 && f3[0].Seq == 1 && f3[0].Items[1] == "y" && f3[0].Bytes == 2 && pn3 == 0)

	// 5. age measured from first item; empty tick is a no-op.
	base := time.Now()
	s5 := &sink{}
	b5 := batcher.New(s5, 9, 1<<20, 10*time.Second, func() time.Time { return now })
	now = base
	b5.Tick()
	_ = b5.Add("a")
	now = base.Add(9 * time.Second)
	_ = b5.Add("b")
	b5.Tick()
	mid := len(s5.batches)
	now = base.Add(11 * time.Second)
	b5.Tick()
	ok("5 age from first item, no empty batch", mid == 0 && len(s5.batches) == 1)

	// 6. flush empty no-op; close flushes, then ErrClosed, idempotent.
	s6 := &sink{}
	b6 := batcher.New(s6, 9, 1<<20, time.Hour, nil)
	_ = b6.Flush()
	_ = b6.Add("a")
	c1 := b6.Close()
	c2 := b6.Close()
	ok("6 flush/close semantics", len(s6.batches) == 1 && c1 == nil && c2 == nil &&
		b6.Add("z") == batcher.ErrClosed && b6.Flush() == batcher.ErrClosed)

	// 7. during Deliver the batch is neither pending nor failed.
	s7 := &sink{}
	b7 := batcher.New(s7, 2, 1<<20, time.Hour, nil)
	vis := false
	s7.hook = func() {
		n, by := b7.Pending()
		vis = n == 0 && by == 0 && len(b7.Failed()) == 0
	}
	_ = b7.Add("a")
	_ = b7.Add("b")
	ok("7 in-flight batch invisible", vis)

	// 8. concurrent conservation: delivered+failed+pending == accepted.
	s8 := &failSometimes{}
	b8 := batcher.New(s8, 5, 1<<20, time.Millisecond, nil)
	var wg sync.WaitGroup
	accepted := 0
	var mu sync.Mutex
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				if err := b8.Add("item"); err == nil || err.Error() == "boom" {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	_ = b8.Close()
	got := 0
	for _, bt := range s8.all() {
		got += len(bt.Items)
	}
	pn8, _ := b8.Pending()
	ok("8 concurrent conservation", got+pn8 == accepted)
}

type failSometimes struct {
	mu   sync.Mutex
	n    int
	seen []batcher.Batch
}

func (f *failSometimes) Deliver(b batcher.Batch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	f.seen = append(f.seen, b)
	if f.n%7 == 0 {
		return errors.New("boom")
	}
	return nil
}

func (f *failSometimes) all() []batcher.Batch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]batcher.Batch(nil), f.seen...)
}
