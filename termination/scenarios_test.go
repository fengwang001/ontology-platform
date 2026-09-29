package termination

import (
	"errors"
	"testing"
)

// TestLateMessageAfterToken 关键反误报场景：令牌经过进程 1 之后，
// 在途消息才到达 1，使 1 重新活跃并变黑；本轮绝不能宣告终止，
// 系统真正静止后至多再用一轮宣告。
func TestLateMessageAfterToken(t *testing.T) {
	const n = 3
	d := mustNew(t, n)

	id, err := d.Send(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	idleEveryProcess(t, d, n)
	if _, r, err := d.PassToken(0); err != nil || r != 1 {
		t.Fatalf("start round: r=%d err=%v", r, err)
	}

	if _, _, err := d.PassToken(2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Deliver(id); err != nil {
		t.Fatal(err)
	}

	// 计数和恰好为 0（count[0]=1, count[1]=-1），但 1 在令牌经过后才变黑，
	// 颜色与状态必须阻止误报。令牌此刻已在 0 手中。
	ann, r, _ := d.PassToken(0)
	if ann {
		t.Fatalf("false termination in round %d with reactivated process", r)
	}

	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	ann, _ = ringToInitiator(t, d, n) // 1 为黑的一轮
	if ann {
		t.Fatal("false termination: black process visited this round")
	}
	ann, r = ringToInitiator(t, d, n) // 全员自始至终为白、计数 0
	if !ann {
		t.Fatal("termination not announced after the system became quiescent")
	}
	assertQuiescent(t, d, n)
	t.Logf("termination announced at round %d (within 2 rounds after quiescence)", r)
}

// TestMessageTokenRace 2->1 的消息在令牌经过 1 后才到达。
func TestMessageTokenRace(t *testing.T) {
	const n = 4
	d := mustNew(t, n)

	id, err := d.Send(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	for p := 0; p < n; p++ {
		if p != 2 {
			if err := d.BecomeIdle(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := d.BecomeIdle(2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}

	for _, p := range []int{3, 2, 1} {
		if _, _, err := d.PassToken(p); err != nil {
			t.Fatalf("PassToken(%d): %v", p, err)
		}
	}
	if _, err := d.Deliver(id); err != nil {
		t.Fatal(err)
	}
	ann, _, _ := d.PassToken(0)
	if ann {
		t.Fatal("false termination in message/token race")
	}

	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	ann, _ = ringToInitiator(t, d, n)
	if ann {
		t.Fatal("false termination with black 1")
	}
	ann, r := ringToInitiator(t, d, n)
	if !ann {
		t.Fatal("termination not announced after race resolved")
	}
	assertQuiescent(t, d, n)
	if r > 3 {
		t.Fatalf("announced round %d exceeds the two-round bound after quiescence", r)
	}
}

// TestTerminationWithinTwoRounds 令牌位于环中段时系统静止：
// 静止所在轮或其后至多一轮内必须宣告。
func TestTerminationWithinTwoRounds(t *testing.T) {
	const n = 5
	d := mustNew(t, n)

	id1, err := d.Send(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := d.Send(3, 2)
	if err != nil {
		t.Fatal(err)
	}
	idleEveryProcess(t, d, n)
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}
	// 令牌停在 4（环中段）；消息此刻才投递，但接收者 3、2 都还在令牌前方，
	// 它们的黑色会在本轮被令牌看到，因此静止所在轮即可宣告。
	if _, err := d.Deliver(id1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Deliver(id2); err != nil {
		t.Fatal(err)
	}
	idleEveryProcess(t, d, n) // 真正静止，令牌仍在 4

	ann, r := drainToken(t, d)
	if !ann {
		ann, r = drainToken(t, d) // 至多再用一轮
		if !ann {
			t.Fatal("not announced within two rounds after quiescence")
		}
	}
	if r > 2 {
		t.Fatalf("announced at round %d, expected <= 2", r)
	}
	assertQuiescent(t, d, n)
}

// TestSingleProcessRing n==1：两次 PassToken 分别发起与结束第 1 轮。
func TestSingleProcessRing(t *testing.T) {
	d := mustNew(t, 1)

	if _, err := d.Send(0, 0); !errors.Is(err, ErrSendToSelf) {
		t.Fatalf("self-send: %v", err)
	}
	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}
	ann, r, err := d.PassToken(0)
	if err != nil {
		t.Fatal(err)
	}
	if ann {
		t.Fatalf("first PassToken only starts round 1, got announcement round %d", r)
	}
	ann, r, err = d.PassToken(0)
	if err != nil {
		t.Fatal(err)
	}
	if !ann || r != 1 {
		t.Fatalf("expected termination at round 1, got ann=%v round=%d", ann, r)
	}
	assertQuiescent(t, d, 1)
}
