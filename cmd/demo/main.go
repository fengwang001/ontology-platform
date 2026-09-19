// demo 演练属性变更分发器的关键语义，逐步打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"

	"ontology"
)

var failures int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func recv(s *ontology.Subscription, n int) []uint64 {
	seqs := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		m, ok := <-s.C()
		if !ok {
			break
		}
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

func sub(d *ontology.Dispatcher, prefix string, opts ontology.SubscribeOptions) *ontology.Subscription {
	s, err := d.Subscribe(prefix, nil, opts)
	if err != nil {
		panic(err)
	}
	return s
}

func publish(d *ontology.Dispatcher, entity string, n int) {
	for i := 0; i < n; i++ {
		if _, err := d.Publish(entity, "name", i); err != nil {
			panic(err)
		}
	}
}

func main() {
	// 1. DropOldest：容量 2 发 5 条，收到最新的 [4 5]，丢弃 3 条。
	d := ontology.New()
	s := sub(d, "user", ontology.SubscribeOptions{Buffer: 2, Overflow: ontology.DropOldest})
	publish(d, "user1", 5)
	got := recv(s, 2)
	check("DropOldest", eq(got, []uint64{4, 5}) && s.Dropped() == 3 && s.LastDroppedSeq() == 3,
		fmt.Sprintf("recv=%v dropped=%d last=%d", got, s.Dropped(), s.LastDroppedSeq()))
	d.Close()

	// 2. DropNewest：容量 2 发 5 条，收到最旧的 [1 2]，新消息被丢弃。
	d = ontology.New()
	s = sub(d, "user", ontology.SubscribeOptions{Buffer: 2, Overflow: ontology.DropNewest})
	publish(d, "user1", 5)
	got = recv(s, 2)
	check("DropNewest", eq(got, []uint64{1, 2}) && s.Dropped() == 3 && s.LastDroppedSeq() == 5,
		fmt.Sprintf("recv=%v dropped=%d last=%d", got, s.Dropped(), s.LastDroppedSeq()))
	d.Close()

	// 3. Disconnect：队列满即断开，已入队的 [1 2] 可读完，随后通道关闭。
	d = ontology.New()
	s = sub(d, "user", ontology.SubscribeOptions{Buffer: 2, Overflow: ontology.Disconnect})
	publish(d, "user1", 5)
	got = recv(s, 3)
	check("Disconnect", eq(got, []uint64{1, 2}) && s.Canceled() && s.Dropped() == 1 && s.LastDroppedSeq() == 3,
		fmt.Sprintf("recv=%v canceled=%v dropped=%d", got, s.Canceled(), s.Dropped()))
	d.Close()

	// 4. 慢订阅者不影响快订阅者收全。
	d = ontology.New()
	slow := sub(d, "user", ontology.SubscribeOptions{Buffer: 1, Overflow: ontology.DropNewest})
	fast := sub(d, "user", ontology.SubscribeOptions{Buffer: 16, Overflow: ontology.DropNewest})
	publish(d, "user1", 6)
	gotFast := recv(fast, 6)
	check("SlowNotBlocking", eq(gotFast, []uint64{1, 2, 3, 4, 5, 6}) && fast.Dropped() == 0 && slow.Dropped() == 5,
		fmt.Sprintf("fast recv=%v dropped=%d, slow dropped=%d", gotFast, fast.Dropped(), slow.Dropped()))
	d.Close()

	// 5. 序号缺口与丢弃数对得上：收到 [1 2 4]，缺口 {3} == 丢弃 1 条。
	d = ontology.New()
	s = sub(d, "user", ontology.SubscribeOptions{Buffer: 2, Overflow: ontology.DropNewest})
	publish(d, "user1", 3)
	got = recv(s, 1) // 取走 1，腾出位置
	publish(d, "user1", 1)
	got = append(got, recv(s, 2)...)
	gaps := ontology.Gaps(got)
	check("GapAccounting", eq(got, []uint64{1, 2, 4}) && ontology.GapCount(got) == s.Dropped(),
		fmt.Sprintf("recv=%v gaps=%v dropped=%d", got, gaps, s.Dropped()))
	d.Close()

	// 6. 前缀 user 不匹配 superuser，但匹配 user1。
	d = ontology.New()
	s = sub(d, "user", ontology.SubscribeOptions{Buffer: 4})
	publish(d, "superuser", 1)
	publish(d, "user1", 1)
	got = recv(s, 1)
	check("PrefixNotSubstring", len(d.Match("superuser", "name")) == 0 && eq(got, []uint64{2}),
		fmt.Sprintf("Match(superuser)=%v recv=%v", d.Match("superuser", "name"), got))
	d.Close()

	// 7. 取消后不再收到，重复取消无害。
	d = ontology.New()
	s = sub(d, "user", ontology.SubscribeOptions{Buffer: 4, Drain: ontology.DropPending})
	publish(d, "user1", 2)
	s.Cancel()
	s.Cancel()
	publish(d, "user1", 2)
	_, ok := <-s.C()
	check("CancelIdempotent", !ok && s.Canceled(), "no messages after cancel, double cancel safe")
	d.Close()

	// 8. 关闭后 Publish 被拒、再订阅失败，关闭幂等。
	d = ontology.New()
	d.Close()
	d.Close()
	_, pubErr := d.Publish("user1", "name", 1)
	_, subErr := d.Subscribe("user", nil, ontology.SubscribeOptions{Buffer: 1})
	check("CloseSemantics", errors.Is(pubErr, ontology.ErrClosed) && errors.Is(subErr, ontology.ErrClosed),
		fmt.Sprintf("publish err=%v, subscribe err=%v", pubErr, subErr))

	fmt.Printf("TOTAL: %d checks, %d failed\n", 8, failures)
}
