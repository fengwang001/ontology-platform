// 演示属性变更订阅与扇出分发器。运行：go run ./cmd/demo
package main

import (
	"fmt"

	"ontology"
)

type report struct {
	pass int
	fail int
}

func (r *report) check(name string, ok bool) {
	if ok {
		r.pass++
		fmt.Printf("OK   %s\n", name)
		return
	}
	r.fail++
	fmt.Printf("FAIL %s\n", name)
}

func seqs(s *ontology.Subscription) []uint64 {
	var out []uint64
	for {
		select {
		case m, open := <-s.C():
			if !open {
				return out
			}
			out = append(out, m.Seq)
		default:
			return out
		}
	}
}

func main() {
	r := &report{}

	// 1) 三种满队列策略：缓冲 2，投递 5 条且不读取。
	d := ontology.New()
	old, _ := d.Subscribe(ontology.SubscriptionConfig{ID: "old", Prefix: "e", Buffer: 2, OnOverflow: ontology.DropOldest})
	new, _ := d.Subscribe(ontology.SubscriptionConfig{ID: "new", Prefix: "e", Buffer: 2, OnOverflow: ontology.DropNewest})
	lag, _ := d.Subscribe(ontology.SubscriptionConfig{ID: "lag", Prefix: "e", Buffer: 2, OnOverflow: ontology.DisconnectLagging})
	for i := 0; i < 5; i++ {
		d.Publish("e", "p", i)
	}
	os := old.Stats()
	ns := new.Stats()
	ls := lag.Stats()
	oldSeqs := seqs(old)
	newSeqs := seqs(new)
	lagSeqs := seqs(lag)
	fmt.Printf("     drop-oldest recv=%v dropped=%d | drop-newest recv=%v dropped=%d | lag recv=%v dropped=%d\n",
		oldSeqs, os.Dropped, newSeqs, ns.Dropped, lagSeqs, ls.Dropped)
	r.check("drop-oldest keeps newest [4 5], dropped=3", eq(oldSeqs, 4, 5) && os.Dropped == 3)
	r.check("drop-newest keeps oldest [1 2], dropped=3", eq(newSeqs, 1, 2) && ns.Dropped == 3)
	r.check("lag disconnects on overflow, dropped=1 lagged=true", ls.Dropped == 1 && ls.Lagged)

	// 2) 慢订阅者不影响快订阅者收全。
	d2 := ontology.New()
	_, _ = d2.Subscribe(ontology.SubscriptionConfig{ID: "slow", Prefix: "e", Buffer: 1, OnOverflow: ontology.DropNewest})
	fast, _ := d2.Subscribe(ontology.SubscriptionConfig{ID: "fast", Prefix: "e", Buffer: 20, OnOverflow: ontology.DropNewest})
	for i := 0; i < 12; i++ {
		d2.Publish("e", "p", i)
	}
	fr := seqs(fast)
	r.check("slow subscriber does not starve fast (fast got all 12)", len(fr) == 12 && fr[11] == 12 && fast.Stats().Dropped == 0)

	// 3) 序号缺口与丢弃数对得上。
	d3 := ontology.New()
	g, _ := d3.Subscribe(ontology.SubscriptionConfig{ID: "g", Prefix: "e", Buffer: 3, OnOverflow: ontology.DropOldest})
	for i := 0; i < 10; i++ {
		d3.Publish("e", "p", i)
	}
	got := seqs(g)
	r.check("seq gaps == dropped count", uint64(10)-uint64(len(got)) == g.Stats().Dropped)

	// 4) 前缀 user 不匹配 superuser。
	d4 := ontology.New()
	d4.Subscribe(ontology.SubscriptionConfig{ID: "u", Prefix: "user", Buffer: 5})
	targets := d4.Targets("superuser", "name")
	r.check("prefix user does not match superuser", len(targets) == 0)
	r.check("prefix user matches user/1", len(d4.Targets("user/1", "name")) == 1)

	// 5) 取消后不再收到，重复取消无害。
	d5 := ontology.New()
	s5, _ := d5.Subscribe(ontology.SubscriptionConfig{ID: "x", Prefix: "e", Buffer: 5})
	d5.Publish("e", "p", 1)
	s5.Unsubscribe()
	s5.Unsubscribe()
	d5.Unsubscribe("x")
	d5.Unsubscribe("never")
	seqAfter, _ := d5.Publish("e", "p", 2)
	r.check("unsubscribe stops delivery, repeat unsubscribe harmless", seqAfter == 2 && len(seqs(s5)) == 0)

	// 6) 关闭后 Publish 被拒且再订阅失败，关闭幂等。
	d6 := ontology.New()
	err1 := d6.Close()
	err2 := d6.Close()
	_, errPub := d6.Publish("e", "p", 1)
	_, errSub := d6.Subscribe(ontology.SubscriptionConfig{ID: "z", Buffer: 1})
	r.check("after close publish/subscribe rejected, close idempotent",
		err1 == nil && err2 == nil && errPub == ontology.ErrClosed && errSub == ontology.ErrClosed)

	fmt.Printf("TOTAL pass=%d fail=%d\n", r.pass, r.fail)
}

func eq(got []uint64, want ...uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
