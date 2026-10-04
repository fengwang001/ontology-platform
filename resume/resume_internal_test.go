package resume

import (
	"errors"
	"testing"
)

// 帧大小取 tick，便于手算前缀和与快照大小。
func appendFrames(t *testing.T, pl *Planner, to int64) {
	t.Helper()
	for tick := pl.ring.Cur() + 1; tick <= to; tick++ {
		if err := pl.Append(tick*10, uint64(tick)); err != nil {
			t.Fatalf("Append(%d): %v", tick, err)
		}
	}
}

func TestRejectLeavesStateUntouched(t *testing.T) {
	pl := New(10, 16, 4, 5000, 2)
	appendFrames(t, pl, 12)
	if err := pl.Join(200, "a"); err != nil {
		t.Fatal(err)
	}

	// 玩家 a 在线：Ack 被拒（超前）前后内部状态一致。
	before := pl.players["a"]
	beforeAck := before.ack
	beforeNow := pl.maxNow
	err := pl.Ack(300, "a", 13)
	if !errors.Is(err, ErrAhead) {
		t.Fatalf("want ErrAhead, got %v", err)
	}
	if before.ack != beforeAck || pl.maxNow != beforeNow {
		t.Fatalf("rejected Ack changed state: ack %d->%d maxNow %d->%d",
			beforeAck, before.ack, beforeNow, pl.maxNow)
	}

	// 时钟回退被拒不推进时钟。
	if err := pl.Append(199, 1); !errors.Is(err, ErrClock) {
		t.Fatalf("want ErrClock, got %v", err)
	}
	if pl.maxNow != beforeNow || pl.ring.Cur() != 12 {
		t.Fatalf("rejected Append changed state")
	}

	// Join 状态不符先于满员：b 加入后 a 仍在场时再次 Join a。
	if err := pl.Join(200, "b"); err != nil {
		t.Fatal(err)
	}
	if err := pl.Join(201, "a"); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState before ErrFull, got %v", err)
	}

	// 旧令牌被拒：a 断线后用未来 gen 重连，状态保持断线宽限内。
	tok, err := pl.Disconnect(202, "a")
	if err != nil || tok != 0 {
		t.Fatalf("Disconnect tok=%d err=%v", tok, err)
	}
	if _, err := pl.Reconnect(203, "a", 9, 12); !errors.Is(err, ErrToken) {
		t.Fatalf("want ErrToken, got %v", err)
	}
	s := pl.players["a"]
	if s.online || s.gen != 0 || s.ack != 0 {
		t.Fatalf("rejected Reconnect changed state: %+v", s)
	}

	// 参数非法最先于玩家不存在。
	if err := pl.Ack(204, "ghost", -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	// 时钟回退先于玩家不存在。
	if err := pl.Ack(1, "ghost", 0); !errors.Is(err, ErrClock) {
		t.Fatalf("want ErrClock, got %v", err)
	}
	// 玩家不存在先于状态不符。
	if _, err := pl.Disconnect(205, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	t.Logf("inputs: rejects (Ack a13, Append t199, Join a, Reconn tok9, Ack ghost -1, Ack ghost t1, Disc ghost); outputs verified unchanged; reason=%q",
		pl.LastReason())
}

func TestAckRollbackOnlyViaReconnect(t *testing.T) {
	pl := New(10, 16, 4, 5000, 2)
	appendFrames(t, pl, 12)
	if err := pl.Join(200, "a"); err != nil {
		t.Fatal(err)
	}
	if err := pl.Ack(201, "a", 10); err != nil {
		t.Fatal(err)
	}
	// 小 ack 成功但不改字段。
	if err := pl.Ack(202, "a", 5); err != nil {
		t.Fatal(err)
	}
	if pl.players["a"].ack != 10 {
		t.Fatalf("smaller Ack must not lower ack, got %d", pl.players["a"].ack)
	}
	tok, err := pl.Disconnect(203, "a")
	if err != nil {
		t.Fatal(err)
	}
	// Reconnect 是 ack 唯一可以变小之处。
	plan, err := pl.Reconnect(204, "a", tok, 3)
	if err != nil {
		t.Fatal(err)
	}
	s := pl.players["a"]
	if !s.online || s.gen != 1 || s.ack != 3 {
		t.Fatalf("reconnect session wrong: %+v", s)
	}
	// have=3：n1=9 > (12-10)+C=6，按代价走快照(10)+[11,12]，
	// 快照大小为帧 1..10 之和 55。
	if plan.Kind != SnapshotKind || plan.SnapTick != 10 ||
		plan.From != 11 || plan.To != 12 ||
		plan.Bytes != sumSizes(1, 10)+sumSizes(11, 12) {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	t.Logf("input: ack10 then stale ack5 then reconnect have=3; output: %s [%d,%d] bytes=%d; basis: %s",
		plan.Kind, plan.From, plan.To, plan.Bytes, pl.LastReason())
}

func TestGraceTieAndDepartedRejoin(t *testing.T) {
	pl := New(10, 16, 4, 5000, 1)
	appendFrames(t, pl, 5)
	if err := pl.Join(1000, "a"); err != nil {
		t.Fatal(err)
	}
	tok, err := pl.Disconnect(1000, "a")
	if err != nil || tok != 0 {
		t.Fatalf("first token must be 0, got %d/%v", tok, err)
	}
	// now = t+G-1：宽限内，旧令牌有效。
	plan, err := pl.Reconnect(5999, "a", 0, 0)
	if err != nil || plan.Kind != Delta {
		t.Fatalf("within-grace reconnect: plan=%+v err=%v", plan, err)
	}
	if pl.players["a"].gen != 1 {
		t.Fatalf("gen should become 1, got %d", pl.players["a"].gen)
	}
	// 再次断线，令牌=1；旧令牌 0 被拒（仍在第二次断线的宽限内）。
	tok2, err := pl.Disconnect(5999, "a")
	if err != nil || tok2 != 1 {
		t.Fatalf("second token must be 1, got %d/%v", tok2, err)
	}
	if _, err := pl.Reconnect(5999, "a", 0, 0); !errors.Is(err, ErrToken) {
		t.Fatalf("stale token must give ErrToken, got %v", err)
	}
	// 拖到第二次断线时刻 + G（取等即离场），重连报状态不符。
	if _, err := pl.Reconnect(10999, "a", 1, 0); !errors.Is(err, ErrState) {
		t.Fatalf("departed at tie reconnect must give ErrState, got %v", err)
	}
	// 离场后 Join：gen 由 1 加为 2，ack 归 0。
	if err := pl.Join(11000, "a"); err != nil {
		t.Fatalf("rejoin after departure: %v", err)
	}
	s := pl.players["a"]
	if !s.online || s.gen != 2 || s.ack != 0 {
		t.Fatalf("rejoined session wrong: %+v", s)
	}
	t.Logf("token sequence: 0 -> success(gen1) -> 1 -> stale0 rejected -> tie departure -> rejoin gen2; basis: %s",
		pl.LastReason())
}

func TestConstructorPanics(t *testing.T) {
	cases := []struct {
		name    string
		p, k, c int
		g       int64
		n       int
	}{
		{"p zero", 0, 10, 0, 1, 1},
		{"k below p", 10, 9, 0, 1, 1},
		{"c negative", 10, 10, -1, 1, 1},
		{"g zero", 10, 10, 0, 0, 1},
		{"n too big", 10, 10, 0, 1, 65},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("New(%v) must panic", tc.name)
				}
			}()
			New(tc.p, tc.k, tc.c, tc.g, tc.n)
		})
	}
}

func sumSizes(from, to int64) uint64 {
	if from > to {
		return 0
	}
	return uint64((from + to) * (to - from + 1) / 2)
}
