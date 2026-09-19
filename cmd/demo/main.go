// Command demo exercises the ontology dispatcher end to end: full-queue
// policies, drop accounting, matching rules, unsubscribe and close
// semantics. It prints one OK/FAIL line per check plus a total.
package main

import (
	"errors"
	"fmt"

	"ontology"
)

var passed, total int

func check(ok bool, format string, args ...any) {
	total++
	tag := "OK  "
	if ok {
		passed++
	} else {
		tag = "FAIL"
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

// drain unsubscribes and returns every received sequence number.
func drain(s *ontology.Subscription) []uint64 {
	s.Unsubscribe()
	var seqs []uint64
	for m := range s.C() {
		seqs = append(seqs, m.Seq)
	}
	return seqs
}

func eq(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func publishN(d *ontology.Dispatcher, n int) {
	for i := 0; i < n; i++ {
		if err := d.Publish("user", "name", i); err != nil {
			panic(err)
		}
	}
}

func main() {
	d := ontology.New()

	oldest, _ := d.Subscribe(ontology.Options{Buffer: 2, OnFull: ontology.DropOldest, DrainOnClose: true})
	publishN(d, 4)
	got := drain(oldest)
	check(eq(got, []uint64{3, 4}) && oldest.Dropped() == 2 && oldest.LastDropSeq() == 2,
		"DropOldest: recv=%v dropped=%d lastDrop=%d", got, oldest.Dropped(), oldest.LastDropSeq())

	newest, _ := d.Subscribe(ontology.Options{Buffer: 2, OnFull: ontology.DropNewest, DrainOnClose: true})
	publishN(d, 4)
	got = drain(newest)
	check(eq(got, []uint64{5, 6}) && newest.Dropped() == 2 && newest.LastDropSeq() == 8,
		"DropNewest: recv=%v dropped=%d lastDrop=%d", got, newest.Dropped(), newest.LastDropSeq())

	disc, _ := d.Subscribe(ontology.Options{Buffer: 1, OnFull: ontology.Disconnect})
	publishN(d, 3)
	got = drain(disc)
	check(disc.Lagged() && len(got) == 0 && disc.Dropped() == 2,
		"Disconnect: lagged=%v recv=%v dropped=%d", disc.Lagged(), got, disc.Dropped())

	slow, _ := d.Subscribe(ontology.Options{Buffer: 1, OnFull: ontology.DropNewest})
	fast, _ := d.Subscribe(ontology.Options{Buffer: 50, DrainOnClose: true})
	publishN(d, 50)
	got = drain(fast)
	check(len(got) == 50 && slow.Dropped() == 49,
		"slow subscriber isolated: fast recv=%d/50 slow dropped=%d", len(got), slow.Dropped())
	slow.Unsubscribe()

	gapSub, _ := d.Subscribe(ontology.Options{Buffer: 3, OnFull: ontology.DropNewest, DrainOnClose: true})
	publishN(d, 10)
	got = drain(gapSub)
	check(10-len(got) == int(gapSub.Dropped()),
		"seq gaps match drops: recv=%v gaps=%d dropped=%d", got, 10-len(got), gapSub.Dropped())

	userSub, _ := d.Subscribe(ontology.Options{EntityPrefix: "user", Buffer: 4, DrainOnClose: true})
	super := len(d.Match("superuser", "name"))
	alice := len(d.Match("user.alice", "name"))
	check(super == 0 && alice == 1, "prefix user: superuser matched=%d user.alice matched=%d", super, alice)

	userSub.Unsubscribe()
	userSub.Unsubscribe()
	d.Publish("user", "name", "x")
	after := drain(userSub)
	check(len(after) == 0 && len(d.Match("user", "name")) == 0,
		"unsubscribe: recv after=%d matched=%d repeat-unsub ok", len(after), len(d.Match("user", "name")))

	d.Close()
	errPub := d.Publish("user", "name", 1)
	_, errSub := d.Subscribe(ontology.Options{})
	check(errors.Is(errPub, ontology.ErrClosed) && errors.Is(errSub, ontology.ErrClosed) && d.Close() == nil,
		"closed: publish=%v subscribe=%v close-again ok", errPub, errSub)

	fmt.Printf("TOTAL %d/%d passed\n", passed, total)
}
