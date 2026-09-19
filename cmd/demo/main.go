// Command demo exercises the ontology change fan-out dispatcher.
// Run with: go run ./cmd/demo
package main

import (
	"errors"
	"fmt"
	"io"

	"ontology"
)

var pass, fail int

func verdict(ok bool, format string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fail++
	} else {
		pass++
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func publishN(d *ontology.Dispatcher, n int) {
	for i := 0; i < n; i++ {
		if _, err := d.Publish(ontology.Change{Entity: "e", Attribute: "a"}); err != nil {
			panic(err)
		}
	}
}

func drain(s *ontology.Subscription) []int64 {
	var seqs []int64
	for {
		d, err := s.Receive()
		if errors.Is(err, io.EOF) {
			return seqs
		}
		seqs = append(seqs, d.Seq)
	}
}

func main() {
	fmt.Println("== ontology change fan-out demo ==")

	// 1. DropOldest: newest survive, oldest are counted as dropped.
	{
		d := ontology.NewDispatcher()
		s, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 3, OnOverflow: ontology.DropOldest, OnCancel: ontology.CancelDrain})
		publishN(d, 5)
		d.Unsubscribe(s)
		got := drain(s)
		verdict(len(got) == 3 && got[0] == 3 && got[2] == 5 && s.DroppedTotal() == 2 && s.LastDroppedSeq() == 2,
			"DropOldest: recv=%v drops=%d lastDropSeq=%d (want [3 4 5], 2, 2)", got, s.DroppedTotal(), s.LastDroppedSeq())
	}

	// 2. DropNewest: queued oldest survive; incoming messages are dropped.
	{
		d := ontology.NewDispatcher()
		s, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 2, OnOverflow: ontology.DropNewest, OnCancel: ontology.CancelDrain})
		publishN(d, 5)
		d.Unsubscribe(s)
		got := drain(s)
		verdict(len(got) == 2 && got[0] == 1 && got[1] == 2 && s.DroppedTotal() == 3 && s.LastDroppedSeq() == 5,
			"DropNewest: recv=%v drops=%d lastDropSeq=%d (want [1 2], 3, 5)", got, s.DroppedTotal(), s.LastDroppedSeq())
	}

	// 3. DropNewestAndDisconnect: marked lagging, removed, buffered still drainable.
	{
		d := ontology.NewDispatcher()
		s, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 2, OnOverflow: ontology.DropNewestAndDisconnect})
		publishN(d, 4)
		got := drain(s)
		verdict(!s.Active() && s.DroppedTotal() == 1 && s.LastDroppedSeq() == 3 && len(got) == 2 && got[1] == 2,
			"Disconnect: active=false recv=%v drops=%d lastDropSeq=%d (want [1 2], 1, 3)", got, s.DroppedTotal(), s.LastDroppedSeq())
	}

	// 4. A slow subscriber must not make another subscriber miss anything.
	{
		d := ontology.NewDispatcher()
		slow, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 1, OnOverflow: ontology.DropOldest})
		fast, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 1000, OnCancel: ontology.CancelDrain})
		publishN(d, 500)
		d.Unsubscribe(fast)
		got := drain(fast)
		verdict(len(got) == 500 && fast.DroppedTotal() == 0 && slow.DroppedTotal() == 499,
			"slow/fast isolation: fast=%d/500 fastDrops=%d slowDrops=%d", len(got), fast.DroppedTotal(), slow.DroppedTotal())
		d.Close()
	}

	// 5. Sequence gaps must equal the per-subscriber drop count.
	{
		d := ontology.NewDispatcher()
		s, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 5, OnOverflow: ontology.DropOldest, OnCancel: ontology.CancelDrain})
		publishN(d, 20)
		d.Unsubscribe(s)
		got := drain(s)
		seen := make(map[int64]bool)
		for _, v := range got {
			seen[v] = true
		}
		var gaps int64
		for v := int64(1); v <= 20; v++ {
			if !seen[v] {
				gaps++
			}
		}
		verdict(gaps == s.DroppedTotal() && len(got) == 5 && got[0] == 16,
			"gap accounting: gaps=%d drops=%d recv=%v", gaps, s.DroppedTotal(), got)
	}

	// 6. Prefix "user" must not match "superuser".
	{
		d := ontology.NewDispatcher()
		s, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 10, EntityPrefix: "user", Attributes: []string{"name"}, OnCancel: ontology.CancelDrain})
		d.Publish(ontology.Change{Entity: "user.1", Attribute: "name"})
		d.Publish(ontology.Change{Entity: "superuser", Attribute: "name"})
		d.Publish(ontology.Change{Entity: "user2", Attribute: "name"})
		d.Unsubscribe(s)
		got := drain(s)
		verdict(len(got) == 1 && got[0] == 1,
			"prefix boundary: recv=%v (only user.1; superuser/user2 rejected)", got)
	}

	// 7. Cancel stops delivery; repeated cancel is harmless; buffered policy honored.
	{
		d := ontology.NewDispatcher()
		s, _ := d.Subscribe(ontology.SubscribeOptions{Buffer: 10, OnCancel: ontology.CancelDrain})
		publishN(d, 2)
		for i := 0; i < 3; i++ {
			if err := d.Unsubscribe(s); err != nil {
				panic(err)
			}
		}
		d.Publish(ontology.Change{Entity: "e", Attribute: "a"})
		got := drain(s)
		_, err := s.Receive()
		verdict(len(got) == 2 && errors.Is(err, io.EOF),
			"unsubscribe: recv=%v then EOF, repeat cancel harmless, no post-cancel msg", got)
	}

	// 8. Close rejects Publish (deterministically), rejects resubscribe, is idempotent.
	{
		d := ontology.NewDispatcher()
		if err := d.Close(); err != nil {
			panic(err)
		}
		_, pErr := d.Publish(ontology.Change{Entity: "e", Attribute: "a"})
		_, sErr := d.Subscribe(ontology.SubscribeOptions{Buffer: 1})
		verdict(d.Close() == nil && errors.Is(pErr, ontology.ErrClosed) && errors.Is(sErr, ontology.ErrClosed),
			"close: publishErr=%v subscribeErr=%v secondClose=nil", pErr, sErr)
	}

	fmt.Printf("== total: %d OK, %d FAIL ==\n", pass, fail)
	if fail > 0 {
		fmt.Println("DEMO FAILED")
	}
}
