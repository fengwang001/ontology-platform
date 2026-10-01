package ledger

import (
	"errors"
	"strings"
	"testing"
)

// 端到端场景：批量确认含边界标签；批量拒绝回队归位且早于更大序号现存消息；
// 回队消息重投标签更大但批量确认仍按标签取范围；重投标志；
// 预取恰好放下与恰好放满。
func TestScenarioAgainstNaiveModel(t *testing.T) {
	const P = 3
	l := New[string](P)
	m := newNaiveModel(P)

	enqueue := func(msg string) {
		got := l.Enqueue(msg)
		want := m.enqueue(msg)
		t.Logf("输入 Enqueue(%q) -> 输出 seq=%d；判定：入队序号只增不复用，模型 seq=%d", msg, got, want)
		if got != uint64(want) {
			t.Fatalf("enqueue seq mismatch %d vs %d", got, want)
		}
		assertEquivalent(t, l, m, "enqueue "+msg)
	}
	deliver := func(why string) {
		d, err := l.Deliver()
		md, r, ok := m.deliver()
		if ok {
			t.Logf("输入 Deliver -> 输出 tag=%d seq=%d msg=%q redeliver=%v；判定：%s",
				d.Tag, d.Seq, d.Message, d.Redeliver, why)
			if err != nil || d.Tag != uint64(md.tag) || d.Seq != uint64(md.seq) ||
				d.Message != md.message || d.Redeliver != md.redeliver {
				t.Fatalf("deliver mismatch: ledger=(%+v,%v) model=(%+v)", d, err, md)
			}
		} else {
			t.Logf("输入 Deliver -> 输出错误 %q；判定：%s（模型原因=%s）", reasonOf(err), why, r)
			var e *Error
			if !errors.As(err, &e) || e.Reason != r {
				t.Fatalf("deliver error mismatch: ledger=%v model=%v", err, r)
			}
		}
		assertEquivalent(t, l, m, "deliver")
	}
	ack := func(tag uint64, multiple bool, why string) {
		err := l.Ack(tag, multiple)
		r, ok := m.ack(int(tag), multiple)
		t.Logf("输入 Ack(tag=%d, multiple=%v) -> 输出 %q；判定：%s", tag, multiple, reasonOf(err), why)
		if ok != (err == nil) || (err != nil && err.(*Error).Reason != r) {
			t.Fatalf("ack mismatch: ledger=%v model=%v/%v", err, r, ok)
		}
		assertEquivalent(t, l, m, "ack")
	}
	nack := func(tag uint64, multiple bool, requeue bool, why string) {
		err := l.Nack(tag, multiple, requeue)
		r, ok := m.nack(int(tag), multiple, requeue)
		t.Logf("输入 Nack(tag=%d, multiple=%v, requeue=%v) -> 输出 %q；判定：%s",
			tag, multiple, requeue, reasonOf(err), why)
		if ok != (err == nil) || (err != nil && err.(*Error).Reason != r) {
			t.Fatalf("nack mismatch: ledger=%v model=%v/%v", err, r, ok)
		}
		assertEquivalent(t, l, m, "nack")
	}

	// 入队 5 条（序号 1..5）；预取 P=3，恰好放下前三条（未确认数 1、2、3）。
	enqueue("m1")
	enqueue("m2")
	enqueue("m3")
	enqueue("m4")
	enqueue("m5")

	deliver("P=3，首次投递取序号最小者，tag=1 redeliver=false，未确认 1<3")
	deliver("tag=2 redeliver=false，未确认 2<3，恰好还能放下")
	deliver("tag=3 redeliver=false，未确认恰好放满 3==P")

	// 批量确认边界标签 tag=2：确认标签 <=2 的全部未确认（tag1、tag2），tag3 不动。
	ack(2, true, "multiple=true 按标签取 [1,2]，含边界标签 2；tag=3 不在范围")

	// 空出名额：m4->tag4，m5->tag5；队列空，未确认重新放满。
	deliver("队列最小 m4，tag=4 redeliver=false")
	deliver("队列最小 m5，tag=5 redeliver=false；未确认 tag 3/4/5")

	// 批量拒绝 tag=4 回队：范围 [1,4] 内未确认者只有 tag3(m3)、tag4(m4)，
	// 二者回队；tag5(m5) 不动。回队按入队序号归位：队列 [m3,m4]。
	nack(4, true, true, "按标签取 [1,4] 中未确认者 tag3(m3)、tag4(m4) 回队；"+
		"归位后 m3(seq3) 在 m4(seq4) 前，且都早于任何更大序号现存消息")
	if got := l.QueuedMessages(); strings.Join(got, ",") != "m3,m4" {
		t.Fatalf("requeue position: got %v want [m3 m4]", got)
	}

	// 重投分配全新且更大的标签：m3->tag6，m4->tag7，redeliver=true。
	deliver("m3 重投 tag=6 redeliver=true；新标签大于未回队者 tag=5")
	deliver("m4 重投 tag=7 redeliver=true")

	// 批量确认按标签而非入队序号：ack(6, multiple) 覆盖 tag<=6 的未确认者，
	// 即 tag5(m5,seq5) 与 tag6(m3,seq3)；tag7(m4,seq4) 虽 seq 更小但标签更大，保留。
	ack(6, true, "按标签取 [1,6]：tag5(m5,seq5)、tag6(m3,seq3) 被确认；"+
		"tag7(m4,seq4) 入队序号更小但标签更大，不在范围")
	if got := l.Unacked(); len(got) != 1 || got[0].Tag != 7 || got[0].Seq != 4 {
		t.Fatalf("after multiple ack, only tag7(seq4) should remain, got %+v", got)
	}

	ack(7, false, "multiple=false 只确认 tag=7 本身")
	deliver("队列空且未确认未满 -> ErrQueueEmpty")

	// 标签非法：0 与超过最大标签。
	ack(0, false, "tag=0 非法 ErrTagIllegal，状态不变")
	ack(99, true, "tag=99 超过最大标签 7，ErrTagIllegal，状态不变")

	// 非批量操作已结算标签。
	ack(1, false, "tag=1 早已确认 ErrAlreadySettled")
	nack(1, false, true, "已确认标签对 Nack 同样 ErrAlreadySettled")

	// 批量但范围为空：tag 合法、[1,t] 内无未确认者。
	ack(7, true, "tag=7 合法但 tag<=7 全部结算 ErrRangeEmpty")
	nack(3, true, false, "tag=3 合法但 [1,3] 无未确认者 ErrRangeEmpty，丢弃数不变")

	// 丢弃路径：不回队拒绝累加丢弃数。
	enqueue("m6")
	deliver("m6 首次投递 tag=8 redeliver=false")
	before := l.Dropped()
	nack(8, false, false, "multiple=false 不回队：m6 永久丢弃，dropped +1")
	if l.Dropped() != before+1 {
		t.Fatalf("dropped not incremented: %d -> %d", before, l.Dropped())
	}

	// 预取已满优先于队列为空。
	enqueue("m7")
	deliver("m7 tag=9，未确认 1")
	deliver("队列空且未满 -> ErrQueueEmpty（优先关系的反向条件）")
	enqueue("m8")
	enqueue("m9")
	deliver("m8 tag=10，未确认 2")
	deliver("m9 tag=11，未确认恰好放满 3")
	deliver("队列空 且 未确认==P：必须 ErrPrefetchFull，优先于 ErrQueueEmpty")

	// 确认一条后队列仍空：未满，错误切回 QueueEmpty。
	ack(9, false, "确认 tag9 后未确认降为 2")
	deliver("未满但队列空 -> ErrQueueEmpty")
}

// 回队归位专项：回队消息插在入队序号更大的现存消息之前，不是队首也不是队尾。
func TestRequeuePositionBySeq(t *testing.T) {
	l := New[string](3)
	for _, msg := range []string{"a", "b", "c", "d", "e"} {
		l.Enqueue(msg)
	}
	d1, _ := l.Deliver() // a tag1
	d2, _ := l.Deliver() // b tag2
	_, _ = l.Deliver()   // c tag3

	if err := l.Nack(d1.Tag, false, true); err != nil { // a 回队
		t.Fatal(err)
	}
	if got := l.QueuedMessages(); strings.Join(got, ",") != "a,d,e" {
		t.Fatalf("got %v, want [a d e]", got)
	}
	if err := l.Nack(d2.Tag, false, true); err != nil { // b 回队
		t.Fatal(err)
	}
	if got := l.QueuedMessages(); strings.Join(got, ",") != "a,b,d,e" {
		t.Fatalf("got %v, want [a b d e]", got)
	}

	// 重投顺序必须严格按入队序号：a(red) -> b(red) -> d -> e。
	rd, err := l.Deliver()
	if err != nil || rd.Message != "a" || !rd.Redeliver {
		t.Fatalf("expected redelivered a, got %+v %v", rd, err)
	}
	rd, err = l.Deliver()
	if err != nil || rd.Message != "b" || !rd.Redeliver {
		t.Fatalf("expected redelivered b, got %+v %v", rd, err)
	}
	// 此时 c(tag3)、a、b 三条未确认恰好放满；结算 a、b 释放名额后，
	// d、e 从未被回队，首次投递标志必须为假。
	if err := l.Ack(rd.Tag, false); err != nil {
		t.Fatal(err)
	}
	prev := l.Unacked()
	var tagA uint64
	for _, u := range prev {
		if u.Message == "a" {
			tagA = u.Tag
		}
	}
	if err := l.Ack(tagA, false); err != nil {
		t.Fatal(err)
	}
	rd, err = l.Deliver()
	if err != nil || rd.Message != "d" || rd.Redeliver {
		t.Fatalf("expected first delivery d, got %+v %v", rd, err)
	}
	rd, err = l.Deliver()
	if err != nil || rd.Message != "e" || rd.Redeliver {
		t.Fatalf("expected first delivery e, got %+v %v", rd, err)
	}
	t.Logf("输入 连续 Deliver -> 输出 a(redeliver=true), b(redeliver=true), d(redeliver=false)；" +
		"判定：回队按 seq 归位，未回队的 d 首次投递标志为假")
}
