package warming

import "testing"

func TestClusterLifecycle(t *testing.T) {
	c := &Cluster{}
	if c.Ready() {
		t.Fatalf("empty cluster not ready")
	}
	c.StartWarming(1, 5)
	if c.Ready() || !c.HasWarming || c.WarmingVersion != 1 || c.Since != 5 {
		t.Fatalf("warming state wrong: %+v", c)
	}
	if !c.TimedOut(10, 15) {
		t.Fatalf("since+W==now must time out")
	}
	if c.TimedOut(10, 14) {
		t.Fatalf("one ms before must not time out")
	}
	c.Promote()
	if !c.Ready() || c.ServingVersion != 1 || c.HasWarming {
		t.Fatalf("promoted state wrong: %+v", c)
	}
	// 更新进入新预热：有在役、有预热，未就绪。
	c.StartWarming(2, 20)
	if c.Ready() {
		t.Fatalf("re-warming not ready")
	}
	if !c.TimedOut(10, 30) {
		t.Fatalf("v2 must time out at 30")
	}
	if !c.FailWarming() {
		t.Fatalf("cluster keeps old serving after fail")
	}
	if !c.Ready() || c.ServingVersion != 1 {
		t.Fatalf("fallback serving v1 wrong: %+v", c)
	}
}

func TestFailNewCluster(t *testing.T) {
	c := &Cluster{}
	c.StartWarming(7, 0)
	if c.FailWarming() {
		t.Fatalf("new cluster without serving must report gone")
	}
	if c.HasWarming || c.HasServing {
		t.Fatalf("new cluster failed should be empty: %+v", c)
	}
}
