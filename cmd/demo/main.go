// Command demo checks each batcher semantic and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/batcher"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) adv(d time.Duration) { c.t = c.t.Add(d) }

var fails int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		fails++
	}
	fmt.Printf("%-4s %s\n", verdict, name)
}

func main() {
	// 1. Triple trigger: count, bytes, age each seal exactly one batch.
	var n1 int
	c1 := &clock{t: time.Unix(0, 0)}
	b1 := batcher.New(batcher.SinkFunc(func(b batcher.Batch) error { n1++; return nil }), 2, 4, time.Second, c1.now)
	_ = b1.Add("a")
	_ = b1.Add("b") // count trigger
	_ = b1.Add("ccc")
	_ = b1.Add("d") // byte trigger
	_ = b1.Add("e")
	c1.adv(time.Second)
	b1.Tick() // age trigger
	b1.Tick() // empty: no-op
	check("1 triple trigger, no duplicates", n1 == 3)

	// 2. Oversized item rejected without touching the buffer.
	b2 := batcher.New(batcher.SinkFunc(func(b batcher.Batch) error { return nil }), 10, 3, time.Hour, nil)
	_ = b2.Add("ab")
	err2 := b2.Add("toolong")
	i2, by2 := b2.Pending()
	check("2 ErrTooLarge, buffer untouched", errors.Is(err2, batcher.ErrTooLarge) && i2 == 1 && by2 == 2)

	// 3. Seq is gapless from 1; failed batches consume a seq.
	var seqs []uint64
	b3 := batcher.New(batcher.SinkFunc(func(b batcher.Batch) error {
		seqs = append(seqs, b.Seq)
		if b.Seq == 2 {
			return errors.New("boom")
		}
		return nil
	}), 1, 100, time.Hour, nil)
	_, _, _ = b3.Add("a"), b3.Add("b"), b3.Add("c")
	check("3 seq 1..N gapless, failed keeps seq", fmt.Sprint(seqs) == "[1 2 3]" && b3.Failed()[0].Seq == 2)

	// 4. Failed batch preserved as-is, not rebuffered.
	f4 := b3.Failed()
	i4, _ := b3.Pending()
	check("4 failed batch intact, no reflow", len(f4) == 1 && f4[0].Items[0] == "b" && f4[0].Bytes == 1 && i4 == 0)

	// 5. maxAge measured from the first buffered item.
	var aged []batcher.Batch
	c5 := &clock{t: time.Unix(0, 0)}
	b5 := batcher.New(batcher.SinkFunc(func(b batcher.Batch) error { aged = append(aged, b); return nil }), 9, 99, 10*time.Second, c5.now)
	_ = b5.Add("x")
	c5.adv(9 * time.Second)
	_ = b5.Add("y")
	c5.adv(2 * time.Second)
	b5.Tick()
	check("5 age from first item, empty Tick no-op", len(aged) == 1 && len(aged[0].Items) == 2)

	// 6. Flush empty no-op; Close flushes once, then ErrClosed, idempotent.
	var n6 int
	b6 := batcher.New(batcher.SinkFunc(func(b batcher.Batch) error { n6++; return nil }), 9, 99, time.Hour, nil)
	_ = b6.Flush()
	_ = b6.Add("z")
	_ = b6.Close()
	_ = b6.Close()
	ok6 := n6 == 1 && errors.Is(b6.Add("q"), batcher.ErrClosed) && errors.Is(b6.Flush(), batcher.ErrClosed)
	check("6 flush/close semantics", ok6)

	// 7. During Deliver the batch is neither pending nor failed.
	var b7 *batcher.Batcher
	var p7, f7 int
	b7 = batcher.New(batcher.SinkFunc(func(b batcher.Batch) error {
		p7, _ = b7.Pending()
		f7 = len(b7.Failed())
		return errors.New("boom")
	}), 1, 99, time.Hour, nil)
	_ = b7.Add("m")
	check("7 in-flight batch invisible", p7 == 0 && f7 == 0 && len(b7.Failed()) == 1)

	// 8. Concurrent use conserves every accepted message.
	var mu sync.Mutex
	delivered := 0
	b8 := batcher.New(batcher.SinkFunc(func(b batcher.Batch) error {
		if b.Seq%5 == 0 {
			return errors.New("boom")
		}
		mu.Lock()
		delivered += len(b.Items)
		mu.Unlock()
		return nil
	}), 4, 64, time.Millisecond, nil)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = b8.Add(fmt.Sprintf("g%d-%d", g, i))
				if i%10 == 0 {
					b8.Tick()
				}
			}
		}(g)
	}
	wg.Wait()
	_ = b8.Close()
	failed8 := 0
	for _, fb := range b8.Failed() {
		failed8 += len(fb.Items)
	}
	p8, _ := b8.Pending()
	check("8 conservation under concurrency", delivered+failed8+p8 == 800)

	fmt.Printf("summary: %d failed of 8\n", fails)
}
