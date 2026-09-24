package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/batcher"
	"ontology/req"
)

func main() {
	r := req.New(nil)
	r.Complete(1, req.ErrClosed)
	ok := r.Seq() == 1 && errors.Is(r.Err(), req.ErrClosed) && len(r.Payload) == 0
	fmt.Printf("%s req result delivery\n", mark(ok))
	if !ok {
		panic("demo failed")
	}
	clock := batcher.NewFakeClock()
	group := batcher.New(batcher.Config{MaxCount: 10, MaxBytes: 100, MaxWait: time.Millisecond}, clock)
	if err := group.Add(req.New(nil)); err != nil {
		panic(err)
	}
	clock.Advance(time.Millisecond)
	batch := <-group.Batches()
	group.Close()
	ok = len(batch.Requests) == 1
	fmt.Printf("%s timeout batcher trigger\n", mark(ok))
	if !ok {
		panic("demo failed")
	}
}

func mark(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}
