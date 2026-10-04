package cmdq

import (
	"fmt"
	"reflect"
	"testing"

	"ontology/wake"
)

func newQ(bw, qcap int64) *Queue {
	return New(wake.New(wake.Params{P: 100, O: 10, W: 5}), bw, qcap)
}

func ids(r Delivery) []string { return r.IDs }

// 题目主示例（P=100,o=10,w=5,K=2,Bw=100,R=2）。
func TestSpecExample(t *testing.T) {
	q := newQ(100, 100)
	must(t, q.Enqueue("c1", 60, 1, 200, 0))
	must(t, q.Enqueue("c2", 60, 1, 112, 0))
	must(t, q.Enqueue("c3", 30, 0, 500, 0))
	must(t, q.Enqueue("c4", 40, 2, 12, 0))

	// expire=10 的指令：下一可用时刻恰为 10，10<=10 不可达。
	if err := q.Enqueue("c5", 10, 0, 10, 0); err != ErrUnreachable {
		t.Fatalf("expire==next-available: got %v want ErrUnreachable", err)
	}

	r1 := q.Deliver(10, 12, 2, 100, 2)
	if !reflect.DeepEqual(r1.Expired, []string{"c4"}) || !reflect.DeepEqual(r1.IDs, []string{"c1"}) {
		t.Fatalf("Deliver(12)=%+v want expired c4, ids c1", r1)
	}
	r2 := q.Deliver(10, 14, 2, 100, 2)
	if len(r2.IDs) != 0 {
		t.Fatalf("same window redeliver: got %v want empty", r2.IDs)
	}
	if err := q.Ack("c1", 50); err != nil {
		t.Fatalf("ack c1: %v", err)
	}
	r3 := q.Deliver(110, 111, 2, 100, 2)
	if !reflect.DeepEqual(r3.IDs, []string{"c2", "c3"}) {
		t.Fatalf("Deliver(111)=%v want [c2 c3]", r3.IDs)
	}
	r4 := q.Deliver(210, 210, 2, 100, 2)
	if !reflect.DeepEqual(r4.Expired, []string{"c2"}) || !reflect.DeepEqual(r4.IDs, []string{"c3"}) {
		t.Fatalf("Deliver(210)=%+v want expired c2, redeliver c3", r4)
	}
	r5 := q.Deliver(310, 310, 2, 100, 2)
	if !reflect.DeepEqual(r5.Failed, []string{"c3"}) || len(r5.IDs) != 0 {
		t.Fatalf("Deliver(310)=%+v want c3 failed, empty ids", r5)
	}
	if c, _ := q.Snapshot("c3"); c.Status != StatusFailed || c.Sends != 2 {
		t.Fatalf("c3 final=%+v want failed sends=2", c)
	}
}

// expire 恰等即过期（投递入口与 Ack）。
func TestExpireEquality(t *testing.T) {
	q := newQ(100, 10)
	must(t, q.Enqueue("a", 10, 0, 100, 0))
	r := q.Deliver(10, 100, 2, 100, 2) // now==expire：先过期，窗口内也无指令
	if !reflect.DeepEqual(r.Expired, []string{"a"}) {
		t.Fatalf("expire equality at deliver: %+v", r)
	}
	q2 := newQ(100, 10)
	must(t, q2.Enqueue("b", 10, 0, 100, 0))
	q2.Deliver(10, 10, 2, 100, 2)
	if err := q2.Ack("b", 100); err != ErrNoCmd {
		t.Fatalf("ack expired command: got %v want ErrNoCmd", err)
	}
}

// 队首阻塞：第一条放不下即停，不跳过取更小的。
func TestHeadOfLineBlocking(t *testing.T) {
	q := newQ(100, 10)
	must(t, q.Enqueue("big", 90, 0, 500, 0))
	must(t, q.Enqueue("small1", 20, 0, 500, 0))
	must(t, q.Enqueue("small2", 20, 0, 500, 0))
	r := q.Deliver(10, 10, 3, 100, 2)
	if !reflect.DeepEqual(r.IDs, []string{"big"}) {
		t.Fatalf("blocking: got %v want [big] only", r.IDs)
	}
	if r.Examined != 2 {
		t.Fatalf("examined=%d want 2 (independent of queue length)", r.Examined)
	}
}

// 第 R 次投递的当窗口不判失败，须等之后窗口的 Deliver。
func TestFailureTiming(t *testing.T) {
	q := newQ(100, 10)
	must(t, q.Enqueue("a", 10, 0, 5000, 0))
	q.Deliver(10, 10, 2, 100, 2)
	r2 := q.Deliver(110, 110, 2, 100, 2) // 第 2 次投递，当窗口仍不失败
	if !reflect.DeepEqual(r2.IDs, []string{"a"}) || len(r2.Failed) != 0 {
		t.Fatalf("second send: %+v", r2)
	}
	r3 := q.Deliver(210, 210, 2, 100, 2) // 之后窗口才 Failed
	if !reflect.DeepEqual(r3.Failed, []string{"a"}) {
		t.Fatalf("failure timing: %+v", r3)
	}
}

// 同窗口 K/Bw 额度跨多次调用共享。
func TestWindowQuotaShared(t *testing.T) {
	q := newQ(100, 10)
	must(t, q.Enqueue("a", 60, 0, 500, 0))
	must(t, q.Enqueue("b", 60, 0, 500, 0))
	r1 := q.Deliver(10, 11, 2, 100, 2)
	r2 := q.Deliver(10, 12, 2, 100, 2)
	if !reflect.DeepEqual(r1.IDs, []string{"a"}) || len(r2.IDs) != 0 {
		t.Fatalf("shared byte quota: %v %v", r1.IDs, r2.IDs)
	}
	q3 := newQ(100, 10)
	for _, id := range []string{"a", "b", "c"} {
		must(t, q3.Enqueue(id, 1, 0, 500, 0))
	}
	d1 := q3.Deliver(10, 10, 2, 100, 2)
	d2 := q3.Deliver(10, 13, 2, 100, 2)
	if len(d1.IDs) != 2 || len(d2.IDs) != 0 {
		t.Fatalf("shared count quota: %v %v", d1.IDs, d2.IDs)
	}
}

func TestAcceptanceOrder(t *testing.T) {
	q := newQ(100, 10)
	if err := q.Enqueue("a", 101, 0, 200, 0); err != ErrTooBig {
		t.Fatalf("too big: %v", err)
	}
	must(t, q.Enqueue("dup", 10, 0, 200, 0))
	// 活动 id 重复先于 too-big 判定。
	if err := q.Enqueue("dup", 101, 0, 200, 0); err != ErrDupCmd {
		t.Fatalf("dup before toobig: %v", err)
	}
	// 下一个可用时刻 s 之后过期：不可达；拒绝不改状态（仍可在更早时刻再投）。
	if err := q.Enqueue("x", 10, 0, 5, 60); err != ErrUnreachable { // s=110
		t.Fatalf("unreachable: %v", err)
	}
	small := New(wake.New(wake.Params{P: 100, O: 10, W: 5}), 100, 2)
	must(t, small.Enqueue("a", 1, 0, 500, 0))
	must(t, small.Enqueue("b", 1, 0, 500, 0))
	if err := small.Enqueue("c", 1, 0, 5, 0); err != ErrUnreachable { // 先不可达
		t.Fatalf("unreachable before full: %v", err)
	}
	if err := small.Enqueue("c", 1, 0, 500, 0); err != ErrFull {
		t.Fatalf("full: %v", err)
	}
}

// examined 与队列长度无关：100 与 10000 两档完全相同。
// 队首 61 字节先投，40 字节的次条放不下即停：examined 恒为 2，
// 满足 examined <= 投出(1)+过期(0)+失败(0)+1 = 2。
func TestExaminedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		q := newQ(100, int64(n))
		must(t, q.Enqueue("head", 61, 0, 50000, 0))
		for i := 0; i < n-1; i++ {
			must(t, q.Enqueue(fmt.Sprintf("c%05d", i), 40, 0, 50000, 0))
		}
		r := q.Deliver(10, 10, 100, 100, 2)
		if !reflect.DeepEqual(r.IDs, []string{"head"}) {
			t.Fatalf("n=%d ids=%v want [head]", n, r.IDs)
		}
		if r.Examined != 2 {
			t.Fatalf("n=%d examined=%d want 2, independent of queue length", n, r.Examined)
		}
		if bound := len(r.IDs) + len(r.Expired) + len(r.Failed) + 1; r.Examined > bound {
			t.Fatalf("n=%d examined=%d exceeds bound %d", n, r.Examined, bound)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
