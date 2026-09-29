package dining

import (
	"errors"
	"strings"
	"testing"
)

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %v, got nil", target)
	}
	if !errors.Is(err, target) && !strings.Contains(err.Error(), target.Error()) {
		t.Fatalf("want error matching %v, got %v", target, err)
	}
}

func TestInitialOrientation(t *testing.T) {
	net := NewQueueNetwork()
	c, err := NewCoordinator([]int{2, 5, 9}, [][2]int{{5, 2}, {9, 2}, {9, 5}}, net, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, ev := c.Snapshot()
	for k, e := range ev {
		if e.ForkAt != k[0] {
			t.Fatalf("edge%v: fork must start at smaller endpoint, got %d", k, e.ForkAt)
		}
		if !e.Dirty {
			t.Fatalf("edge%v: initial fork must be dirty", k)
		}
		if e.TokenAt != k[1] {
			t.Fatalf("edge%v: token must start at larger endpoint, got %d", k, e.TokenAt)
		}
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("initial invariants: %v", v)
	}
	if pending := c.Pending(); len(pending) != 0 {
		t.Fatalf("network must start empty, got %+v", pending)
	}
}

func TestRejectBadTopology(t *testing.T) {
	cases := []struct {
		name   string
		procs  []int
		edges  [][2]int
		target error
	}{
		{"self loop", []int{1, 2}, [][2]int{{1, 1}}, ErrSelfLoop},
		{"missing low", []int{1, 2}, [][2]int{{0, 1}}, ErrEndpointMissing},
		{"missing high", []int{1, 2}, [][2]int{{1, 3}}, ErrEndpointMissing},
		{"duplicate same", []int{1, 2, 3}, [][2]int{{1, 2}, {1, 2}}, ErrDuplicateEdge},
		{"duplicate reversed", []int{1, 2, 3}, [][2]int{{2, 1}, {1, 2}}, ErrDuplicateEdge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCoordinator(tc.procs, tc.edges, NewQueueNetwork(), nil)
			wantErrIs(t, err, tc.target)
		})
	}
}

func TestDirtyForkRelinquishedAndReRequested(t *testing.T) {
	net := NewQueueNetwork()
	c, err := NewCoordinator([]int{0, 1}, [][2]int{{0, 1}}, net, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 双方同时饥饿。p1 先发请求；p0 饥饿时持脏叉，收到请求必须洗净发出，
	// 并立即用收到的令牌再请求。
	if err := c.BecomeHungry(1); err != nil {
		t.Fatal(err)
	}
	if err := c.BecomeHungry(0); err != nil {
		t.Fatal(err)
	}
	pending := c.Pending()
	if len(pending) != 1 || pending[0].Msg.Type != Request || pending[0].From != 1 {
		t.Fatalf("want p1->p0 request, got %+v", pending)
	}
	if err := c.Deliver(pending[0]); err != nil {
		t.Fatal(err)
	}

	// 投递后信道上应有：净叉 p0->p1 与 请求 p0->p1，且 FIFO 中叉在前
	//（“洗净发出”先于“立即再请求”）。
	pending = c.Pending()
	if len(pending) != 2 {
		t.Fatalf("want fork + re-request, got %+v", pending)
	}
	if pending[0].Msg.Type != Fork || pending[0].From != 0 {
		t.Fatalf("first msg must be clean fork from p0, got %+v", pending[0])
	}
	if pending[1].Msg.Type != Request || pending[1].From != 0 {
		t.Fatalf("second msg must be re-request from p0, got %+v", pending[1])
	}

	// 按 FIFO 先送叉、再送请求：p1 拿到净叉吃，p0 的请求因净叉被暂存。
	if err := c.Deliver(pending[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.StartEating(1); err != nil {
		t.Fatal(err)
	}
	if err := c.Deliver(c.Pending()[0]); err != nil { // p0 的请求
		t.Fatal(err)
	}
	_, ev := c.Snapshot()
	e := ev[[2]int{0, 1}]
	if !e.Deferred[1] {
		t.Fatalf("p1 must defer p0's clean-fork request, got %+v", e)
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("invariants: %v", v)
	}

	// p1 吃完：脏化并立即把净叉发给 p0，p0 随后可吃。
	if err := c.FinishEating(1); err != nil {
		t.Fatal(err)
	}
	forks := c.Pending()
	if len(forks) != 1 || forks[0].Msg.Type != Fork {
		t.Fatalf("want deferred clean fork granted, got %+v", forks)
	}
	if err := c.Deliver(forks[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.StartEating(0); err != nil {
		t.Fatal(err)
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("invariants: %v", v)
	}
}

func TestCleanForkAndEatingDefer(t *testing.T) {
	net := NewQueueNetwork()
	c, err := NewCoordinator([]int{0, 1}, [][2]int{{0, 1}}, net, nil)
	if err != nil {
		t.Fatal(err)
	}
	// p1 吃一轮，结束后叉在 p1（脏）、令牌在 p0。
	if err := c.BecomeHungry(1); err != nil {
		t.Fatal(err)
	}
	drain(t, c)
	if err := c.StartEating(1); err != nil {
		t.Fatal(err)
	}
	if err := c.FinishEating(1); err != nil {
		t.Fatal(err)
	}

	// p0 请求脏叉：p1 未进餐，洗净发出；此时 p1 保持思考，不重请求。
	if err := c.BecomeHungry(0); err != nil {
		t.Fatal(err)
	}
	drain(t, c)
	// 净叉现在在 p0。p1 饥饿并请求：p0 持净叉（即使未进餐）也必须暂存。
	if err := c.BecomeHungry(1); err != nil {
		t.Fatal(err)
	}
	drain(t, c)
	_, ev := c.Snapshot()
	e := ev[[2]int{0, 1}]
	if !e.Deferred[0] {
		t.Fatalf("clean fork holder must defer request, got %+v", e)
	}

	// 进餐中暂存：p0 进餐后再让 p1 的请求送达（本次被 eating 分支暂存）。
	if err := c.StartEating(0); err != nil {
		t.Fatal(err)
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("invariants while eating: %v", v)
	}
	if err := c.FinishEating(0); err != nil {
		t.Fatal(err)
	}
	drain(t, c)
	// 结束后净叉应已授予 p1。
	if err := c.StartEating(1); err != nil {
		t.Fatalf("p1 must be able to eat after deferred grant: %v", err)
	}
}

func TestIllegalTransitions(t *testing.T) {
	c, _ := NewCoordinator([]int{0, 1}, [][2]int{{0, 1}}, NewQueueNetwork(), nil)

	// 思考中尝试进餐：缺叉错误。
	// 思考中尝试进餐：不是饥饿态，给独立可区分原因。
	wantErrIs(t, c.StartEating(0), ErrNotHungry)
	// 饥饿退回思考被拒绝。
	if err := c.BecomeHungry(0); err != nil {
		t.Fatal(err)
	}
	wantErrIs(t, c.BecomeThinking(0), ErrHungryToThink)
	// 结束不在进餐中的进程。
	wantErrIs(t, c.FinishEating(0), ErrNotEating)
	wantErrIs(t, c.FinishEating(1), ErrNotEating)
	// 不存在的进程。
	wantErrIs(t, c.BecomeHungry(7), ErrNoSuchProcess)
	wantErrIs(t, c.BecomeThinking(7), ErrNoSuchProcess)
	wantErrIs(t, c.StartEating(7), ErrNoSuchProcess)
	wantErrIs(t, c.FinishEating(7), ErrNoSuchProcess)

	// 思考态显式退回思考：已是思考。
	wantErrIs(t, c.BecomeThinking(1), ErrAlreadyThinking)

	// 缺叉进餐：p1 初始有令牌无叉，饥饿后仍不能进餐。
	if err := c.BecomeHungry(1); err != nil {
		t.Fatal(err)
	}
	wantErrIs(t, c.StartEating(1), ErrMissingFork)
}

func TestDeliverNonexistentMessage(t *testing.T) {
	c, _ := NewCoordinator([]int{0, 1, 2}, [][2]int{{0, 1}, {1, 2}},
		NewQueueNetwork(), nil)

	// 空网络上投递任何消息：拒绝。
	fake := Delivery{From: 1, To: 0, Msg: Message{Edge: [2]int{0, 1}, From: 1, Type: Request}}
	wantErrIs(t, c.Deliver(fake), ErrNoSuchMessage)

	// 真实存在的消息必须按 FIFO 队首投递；伪造队首之外的消息拒绝。
	if err := c.BecomeHungry(1); err != nil {
		t.Fatal(err)
	}
	good := c.Pending()[0]
	bad := Delivery{From: 2, To: 1, Msg: Message{Edge: [2]int{1, 2}, From: 2, Type: Fork}}
	wantErrIs(t, c.Deliver(bad), ErrNoSuchMessage)

	// 边不存在 / 端点不存在 / 信道与消息不匹配：均可区分拒绝。
	wantErrIs(t, c.Deliver(Delivery{From: 1, To: 7, Msg: good.Msg}),
		ErrNoSuchProcess)
	wantErrIs(t, c.Deliver(Delivery{From: 1, To: 0,
		Msg: Message{Edge: [2]int{0, 2}, From: 1, Type: Request}}),
		ErrNoSuchMessage)

	// 被拒绝投递不得改变任何状态：真实消息仍在队首且可成功投递。
	if c.Pending()[0] != good {
		t.Fatalf("rejected delivery must not move the queue, head=%+v want=%+v",
			c.Pending()[0], good)
	}
	if err := c.Deliver(good); err != nil {
		t.Fatalf("legitimate delivery after rejections: %v", err)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	c, _ := NewCoordinator([]int{0, 1}, [][2]int{{0, 1}},
		NewQueueNetwork(), &SliceLogger{})
	p0, e0 := c.Snapshot()

	_ = c.BecomeHungry(7)
	_ = c.StartEating(0)
	_ = c.FinishEating(0)
	_ = c.BecomeThinking(0)
	_ = c.Deliver(Delivery{From: 1, To: 0,
		Msg: Message{Edge: [2]int{0, 1}, From: 1, Type: Fork}})

	p1, e1 := c.Snapshot()
	if len(p0) != len(p1) || p0[0] != p1[0] || p0[1] != p1[1] {
		t.Fatalf("process state changed after rejections: %v -> %v", p0, p1)
	}
	if e0[[2]int{0, 1}] != e1[[2]int{0, 1}] {
		t.Fatalf("edge state changed after rejections:\n%+v\n%+v",
			e0[[2]int{0, 1}], e1[[2]int{0, 1}])
	}
}

// drain 投递当前网络中的全部消息（逐条取队首，保持 FIFO）。
func drain(t *testing.T, c *Coordinator) {
	t.Helper()
	for {
		pending := c.Pending()
		if len(pending) == 0 {
			return
		}
		if err := c.Deliver(pending[0]); err != nil {
			t.Fatalf("unexpected delivery error: %v", err)
		}
	}
}
