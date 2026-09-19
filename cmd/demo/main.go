// Command demo exercises the dispatch package end to end: full-queue
// policies, drop accounting, prefix matching, cancellation, and shutdown.
// It prints one OK/FAIL verdict per check plus a final summary line.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/dispatch"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %-22s %s\n", verdict, name, detail)
}

// collect cancels (Drain) and returns the received seqs.
func collect(s *dispatch.Subscription) []uint64 {
	s.Cancel()
	var seqs []uint64
	for m := range s.C() {
		seqs = append(seqs, m.Seq)
	}
	return seqs
}

func publishAll(d *dispatch.Dispatcher, entity, attr string, n int) {
	for i := 0; i < n; i++ {
		if _, err := d.Publish(entity, attr, i); err != nil {
			fmt.Println("FAIL publish:", err)
			os.Exit(1)
		}
	}
}

func sub(d *dispatch.Dispatcher, opts dispatch.Options) *dispatch.Subscription {
	s, err := d.Subscribe(opts)
	if err != nil {
		fmt.Println("FAIL subscribe:", err)
		os.Exit(1)
	}
	return s
}

func main() {
	d := dispatch.New()

	oldest := sub(d, dispatch.Options{Capacity: 2, OnFull: dispatch.DropOldest})
	newest := sub(d, dispatch.Options{Capacity: 2, OnFull: dispatch.DropNewest})
	disc := sub(d, dispatch.Options{Capacity: 1, OnFull: dispatch.Disconnect})
	publishAll(d, "e", "a", 4)

	got := collect(oldest)
	check("drop-oldest", fmt.Sprint(got) == "[3 4]" && oldest.Dropped() == 2 && oldest.LastDroppedSeq() == 2,
		fmt.Sprintf("recv=%v dropped=%d last=%d", got, oldest.Dropped(), oldest.LastDroppedSeq()))

	got = collect(newest)
	check("drop-newest", fmt.Sprint(got) == "[1 2]" && newest.Dropped() == 2 && newest.LastDroppedSeq() == 4,
		fmt.Sprintf("recv=%v dropped=%d last=%d", got, newest.Dropped(), newest.LastDroppedSeq()))

	got = collect(disc)
	check("disconnect", len(got) == 0 && disc.Lagging() && disc.Dropped() == 2,
		fmt.Sprintf("recv=%v lagging=%v dropped=%d", got, disc.Lagging(), disc.Dropped()))

	// A slow subscriber must not starve a fast one in the same fan-out.
	slow := sub(d, dispatch.Options{Capacity: 1, OnFull: dispatch.DropNewest})
	fast := sub(d, dispatch.Options{Capacity: 16, OnFull: dispatch.DropNewest})
	publishAll(d, "e", "b", 8)
	gotFast := collect(fast)
	gotSlow := collect(slow)
	check("slow-not-blocking", len(gotFast) == 8 && len(gotSlow) == 1 && slow.Dropped() == 7,
		fmt.Sprintf("fast=%d/8 slow=%v dropped=%d", len(gotFast), gotSlow, slow.Dropped()))

	// Sequence gaps account exactly for the drops.
	gap := sub(d, dispatch.Options{Capacity: 2, OnFull: dispatch.DropOldest})
	publishAll(d, "e", "c", 5)
	got = collect(gap)
	missed := uint64(5 - len(got))
	check("seq-gap-accounting", missed == gap.Dropped() && gap.Dropped() == 3,
		fmt.Sprintf("recv=%v missed=%d dropped=%d", got, missed, gap.Dropped()))

	// Prefix "user" must not match "superuser".
	user := sub(d, dispatch.Options{EntityPrefix: "user", Capacity: 8, OnFull: dispatch.DropNewest})
	publishAll(d, "superuser", "x", 1)
	publishAll(d, "user/alice", "x", 1)
	got = collect(user)
	check("prefix-not-substring", len(got) == 1 && len(d.Matches("superuser", "x")) == 0,
		fmt.Sprintf("recv=%v matches(superuser)=%d", got, len(d.Matches("superuser", "x"))))

	// Cancel stops delivery; repeating it is harmless.
	c := sub(d, dispatch.Options{Capacity: 8, OnFull: dispatch.DropNewest})
	publishAll(d, "e", "d", 2)
	c.Cancel()
	c.Cancel()
	publishAll(d, "e", "d", 2)
	n := 0
	for range c.C() {
		n++
	}
	check("cancel-idempotent", n == 2 && c.Dropped() == 0,
		fmt.Sprintf("recv-after-cancel=%d dropped=%d", n-2, c.Dropped()))

	// Close rejects Publish and Subscribe, and is itself idempotent.
	d.Close()
	d.Close()
	_, pubErr := d.Publish("e", "a", nil)
	_, subErr := d.Subscribe(dispatch.Options{Capacity: 1})
	check("close-semantics", errors.Is(pubErr, dispatch.ErrClosed) && errors.Is(subErr, dispatch.ErrClosed),
		fmt.Sprintf("publish=%v subscribe=%v", pubErr, subErr))

	fmt.Printf("TOTAL %d/%d passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
