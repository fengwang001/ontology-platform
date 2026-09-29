package dining

import (
	"testing"
)

func TestSmokeRoundTrip(t *testing.T) {
	net := NewQueueNetwork()
	log := &SliceLogger{}
	c, err := NewCoordinator([]int{0, 1}, [][2]int{{0, 1}}, net, log)
	if err != nil {
		t.Fatal(err)
	}
	// 初始：叉在小端 0（脏），令牌在大端 1。
	_, ev := c.Snapshot()
	e := ev[[2]int{0, 1}]
	if e.ForkAt != 0 || !e.Dirty || e.TokenAt != 1 {
		t.Fatalf("bad init: %+v", e)
	}
	// 1 饥饿 -> 令牌作为请求发给 0。
	if err := c.BecomeHungry(1); err != nil {
		t.Fatal(err)
	}
	pending := c.Pending()
	if len(pending) != 1 || pending[0].Msg.Type != Request {
		t.Fatalf("want 1 request, got %+v", pending)
	}
	if err := c.Deliver(pending[0]); err != nil {
		t.Fatal(err)
	}
	// 0 思考中持脏叉：洗净发出；1 收到净叉即可进餐。
	pending = c.Pending()
	if len(pending) != 1 || pending[0].Msg.Type != Fork {
		t.Fatalf("want clean fork in flight, got %+v", pending)
	}
	if err := c.Deliver(pending[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.StartEating(1); err != nil {
		t.Fatal(err)
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("invariants: %+v", v)
	}
	if err := c.FinishEating(1); err != nil {
		t.Fatal(err)
	}
	// 吃完后叉在 1 处变脏，令牌在 0 处（请求时交付）。
	_, ev = c.Snapshot()
	e = ev[[2]int{0, 1}]
	if e.ForkAt != 1 || !e.Dirty || e.TokenAt != 0 {
		t.Fatalf("bad post-eat: %+v", e)
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("invariants post: %+v", v)
	}
	t.Logf("events=%d", len(log.Snapshot()))
}
