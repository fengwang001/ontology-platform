// Command demo exercises the dispatch package end to end: full-queue
// policies, drop accounting, prefix matching, cancellation and shutdown.
// Every check prints one OK/FAIL line; the last line is the summary.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/dispatch"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func mustSub(d *dispatch.Dispatcher, opts dispatch.SubscribeOptions) *dispatch.Subscriber {
	s, err := d.Subscribe(opts)
	if err != nil {
		fmt.Println("FAIL subscribe", err)
		os.Exit(1)
	}
	return s
}

func publishN(d *dispatch.Dispatcher, entity string, n int) {
	for i := 0; i < n; i++ {
		if err := d.Publish(entity, "status", i); err != nil {
			fmt.Println("FAIL publish", err)
			os.Exit(1)
		}
	}
}

func recvSeqs(s *dispatch.Subscriber, n int) []uint64 {
	seqs := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		m := <-s.Chan()
		seqs = append(seqs, m.Seq)
	}
	return seqs
}

func main() {
	// 1-3. The three full-queue policies, each on a fresh dispatcher.
	d1 := dispatch.New()
	oldest := mustSub(d1, dispatch.SubscribeOptions{
		ID: "oldest", EntityPrefix: "e", BufferSize: 2, Policy: dispatch.DropOldest})
	publishN(d1, "e1", 4)
	got := recvSeqs(oldest, 2)
	check("drop-oldest", fmt.Sprint(got) == "[3 4]" && oldest.Dropped() == 2,
		fmt.Sprintf("recv=%v dropped=%d lastDropSeq=%d", got, oldest.Dropped(), oldest.LastDropSeq()))
	d1.Close()

	d2 := dispatch.New()
	newest := mustSub(d2, dispatch.SubscribeOptions{
		ID: "newest", EntityPrefix: "e", BufferSize: 2, Policy: dispatch.DropNewest})
	publishN(d2, "e1", 4)
	got = recvSeqs(newest, 2)
	check("drop-newest", fmt.Sprint(got) == "[1 2]" && newest.Dropped() == 2,
		fmt.Sprintf("recv=%v dropped=%d lastDropSeq=%d", got, newest.Dropped(), newest.LastDropSeq()))
	d2.Close()

	d3 := dispatch.New()
	disc := mustSub(d3, dispatch.SubscribeOptions{
		ID: "disc", EntityPrefix: "e", BufferSize: 1, Policy: dispatch.Disconnect})
	publishN(d3, "e1", 3)
	got = recvSeqs(disc, 1)
	<-disc.Done()
	check("disconnect", disc.Lagged() && fmt.Sprint(got) == "[1]" && disc.Dropped() == 1,
		fmt.Sprintf("recv=%v lagged=%v dropped=%d", got, disc.Lagged(), disc.Dropped()))
	d3.Close()

	// 4-5. A slow subscriber neither blocks the producer nor starves a
	// fast one; sequence gaps equal the drop count.
	d4 := dispatch.New()
	slow := mustSub(d4, dispatch.SubscribeOptions{
		ID: "slow", EntityPrefix: "e", BufferSize: 1, Policy: dispatch.DropNewest})
	fast := mustSub(d4, dispatch.SubscribeOptions{
		ID: "fast", EntityPrefix: "e", BufferSize: 16, Policy: dispatch.DropNewest})
	publishN(d4, "e1", 8)
	fastGot := recvSeqs(fast, 8)
	check("slow-not-blocking", fmt.Sprint(fastGot) == "[1 2 3 4 5 6 7 8]" && fast.Dropped() == 0,
		fmt.Sprintf("fast recv=%v dropped=%d", fastGot, fast.Dropped()))
	slowGot := recvSeqs(slow, 1)
	gapOK := uint64(len(slowGot))+slow.Dropped() == 8 && slow.LastDropSeq() == 8
	check("seq-gap-accounting", gapOK,
		fmt.Sprintf("slow recv=%v dropped=%d recv+dropped=%d published=8",
			slowGot, slow.Dropped(), uint64(len(slowGot))+slow.Dropped()))
	d4.Close()

	// 6. Prefix matching never degrades to substring matching.
	d5 := dispatch.New()
	user := mustSub(d5, dispatch.SubscribeOptions{ID: "user", EntityPrefix: "user", BufferSize: 4})
	_ = d5.Publish("superuser", "status", 1)
	_ = d5.Publish("user:7", "status", 2)
	got = recvSeqs(user, 1)
	check("prefix-not-substring", fmt.Sprint(got) == "[2]",
		fmt.Sprintf("superuser rejected, user:7 recv=%v", got))
	d5.Close()

	// 7. Cancel stops delivery; repeating it is harmless.
	d6 := dispatch.New()
	sub := mustSub(d6, dispatch.SubscribeOptions{ID: "sub", EntityPrefix: "e", BufferSize: 4})
	_ = d6.Publish("e1", "status", 1)
	sub.Cancel()
	sub.Cancel()
	_ = d6.Publish("e1", "status", 2)
	_, more := <-sub.Chan()
	check("cancel-idempotent", !more && sub.Dropped() == 1,
		fmt.Sprintf("no delivery after cancel, discarded=%d", sub.Dropped()))
	d6.Close()

	// 8. After Close: Publish and Subscribe are rejected detectably.
	d7 := dispatch.New()
	_ = d7.Close()
	pubErr := d7.Publish("e1", "status", 1)
	_, subErr := d7.Subscribe(dispatch.SubscribeOptions{ID: "late"})
	closeAgain := d7.Close()
	check("close-semantics", errors.Is(pubErr, dispatch.ErrClosed) &&
		errors.Is(subErr, dispatch.ErrClosed) && closeAgain == nil,
		fmt.Sprintf("publish=%v subscribe=%v reclose=%v", pubErr, subErr, closeAgain))

	// Summary.
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
