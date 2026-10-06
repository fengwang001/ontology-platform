package floorcontrol_test

import (
	"testing"

	fc "ontology/floorcontrol"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	got = classify(got)
	if want == nil {
		if got != nil {
			t.Fatalf("got %v, want success", got)
		}
		return
	}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func newRoom(t *testing.T, s int64, q int) *fc.Room {
	t.Helper()
	r, err := fc.New(s, q)
	mustOK(t, err)
	return r
}

func snap(t *testing.T, r *fc.Room, now int64) fc.Snapshot {
	t.Helper()
	s, err := r.Snapshot(now)
	mustOK(t, err)
	return s
}

// TestExpireExact：恰等于到期时刻即到期，差一秒未到期。
func TestExpireExact(t *testing.T) {
	r := newRoom(t, 10, 10)
	mustOK(t, r.Join("h", 0))
	mustOK(t, r.Join("a", 0))
	mustOK(t, r.Raise("a", 0))
	mustOK(t, r.Grant("h", 0))

	s := snap(t, r, 9)
	if s.Speaker != "a" || s.Remaining != 1 {
		t.Fatalf("at t=9 expected a/1s, got %q rem=%d", s.Speaker, s.Remaining)
	}
	s = snap(t, r, 10)
	if s.Speaker != "" || s.Remaining != 0 {
		t.Fatalf("at t=10 expected expiry, got %q rem=%d", s.Speaker, s.Remaining)
	}

	// 手动 Grant 授予时刻取该操作的 now（时钟不能回退）。
	mustOK(t, r.Raise("a", 10))
	mustOK(t, r.Grant("h", 20))
	s = snap(t, r, 29)
	if s.Speaker != "a" || s.Remaining != 1 {
		t.Fatalf("manual grant at 20: got %q rem=%d", s.Speaker, s.Remaining)
	}
	s = snap(t, r, 30)
	if s.Speaker != "" {
		t.Fatalf("manual grant must expire at 30, got %q", s.Speaker)
	}
}

// TestMultiRoundExpiry：一次操作连续触发多轮顺延；授予时刻取到期时刻。
func TestMultiRoundExpiry(t *testing.T) {
	r := newRoom(t, 10, 10)
	mustOK(t, r.Join("h", 0))
	for _, u := range []string{"a", "b", "c"} {
		mustOK(t, r.Join(u, 0))
		mustOK(t, r.Raise(u, 0))
	}
	mustOK(t, r.Grant("h", 0)) // a: [0,10], 队列 [b c]

	s := snap(t, r, 30) // a→b@10→c@20→空@30
	if s.Speaker != "" || len(s.Queue) != 0 {
		t.Fatalf("expected all expired at 30, got speaker=%q queue=%v", s.Speaker, s.Queue)
	}

	// 授予时刻取到期时刻而非操作 now：
	// a 在 t=5 授予（到期15），d 排队；t=20 查询时 d 应在 15 获权（到期25），剩余 5。
	r2 := newRoom(t, 10, 10)
	mustOK(t, r2.Join("h", 0))
	mustOK(t, r2.Join("a", 0))
	mustOK(t, r2.Join("d", 0))
	mustOK(t, r2.Raise("a", 0))
	mustOK(t, r2.Raise("d", 0))
	mustOK(t, r2.Grant("h", 5))
	s2 := snap(t, r2, 20)
	if s2.Speaker != "d" || s2.Remaining != 5 {
		t.Fatalf("d must inherit at expire time 15: speaker=%q rem=%d", s2.Speaker, s2.Remaining)
	}
}

// TestMuteSpeakerAndQueued：静音发言者立即顺延；静音队列成员被移出。
func TestMuteSpeakerAndQueued(t *testing.T) {
	r := newRoom(t, 10, 10)
	mustOK(t, r.Join("h", 0))
	for _, u := range []string{"a", "b", "c"} {
		mustOK(t, r.Join(u, 0))
		mustOK(t, r.Raise(u, 0))
	}
	mustOK(t, r.Grant("h", 0)) // a 发言，队列 [b c]

	mustOK(t, r.Mute("h", "b", 1))
	s := snap(t, r, 1)
	if s.Speaker != "a" || len(s.Queue) != 1 || s.Queue[0] != "c" {
		t.Fatalf("after muting queued b: speaker=%q queue=%v", s.Speaker, s.Queue)
	}

	mustOK(t, r.Mute("h", "a", 2))
	s = snap(t, r, 2)
	if s.Speaker != "c" || s.Remaining != 10 || len(s.Queue) != 0 {
		t.Fatalf("after muting speaker a: speaker=%q rem=%d queue=%v", s.Speaker, s.Remaining, s.Queue)
	}

	wantErr(t, r.Raise("a", 2), errMutedRaise)
	mustOK(t, r.Unmute("h", "a", 3))
	mustOK(t, r.Raise("a", 3))
	wantErr(t, r.Mute("h", "h", 4), errTargetHost)
	mustOK(t, r.Mute("h", "c", 4)) // c 此时发言，静音成功
	wantErr(t, r.Mute("h", "c", 5), errAlreadyMuted)
	wantErr(t, r.Unmute("h", "a", 6), errNotMuted) // a 解除后未静音
}

// TestHostSuccession：主持人移交的三个优先级与关闭。
func TestHostSuccession(t *testing.T) {
	// 协管员中最早加入者优先。
	r := newRoom(t, 10, 10)
	mustOK(t, r.Join("h", 0))
	mustOK(t, r.Join("a1", 1))
	mustOK(t, r.Join("c1", 2))
	mustOK(t, r.Join("a2", 3))
	mustOK(t, r.Join("c2", 4))
	mustOK(t, r.Appoint("h", "c2", 5))
	mustOK(t, r.Appoint("h", "c1", 6))
	mustOK(t, r.Leave("h", 7))
	if s := snap(t, r, 7); s.Host != "c1" {
		t.Fatalf("earliest co-manager expected, got %q", s.Host)
	}

	// 无协管员时取最早加入的普通成员。
	r2 := newRoom(t, 10, 10)
	mustOK(t, r2.Join("h", 0))
	mustOK(t, r2.Join("a1", 1))
	mustOK(t, r2.Join("a2", 2))
	mustOK(t, r2.Leave("h", 3))
	if s := snap(t, r2, 3); s.Host != "a1" {
		t.Fatalf("earliest attendee expected, got %q", s.Host)
	}

	// 移交不改变发言权与队列。
	r3 := newRoom(t, 10, 10)
	mustOK(t, r3.Join("h", 0))
	mustOK(t, r3.Join("x", 1))
	mustOK(t, r3.Join("q1", 2))
	mustOK(t, r3.Raise("x", 3))
	mustOK(t, r3.Raise("q1", 3))
	mustOK(t, r3.Grant("h", 3))
	mustOK(t, r3.Leave("h", 4))
	s := snap(t, r3, 4)
	if s.Host != "x" || s.Speaker != "x" || len(s.Queue) != 1 || s.Queue[0] != "q1" {
		t.Fatalf("succession must preserve floor/queue: %+v", s)
	}

	// 无其他成员 → 关闭，之后一切操作报已关闭。
	r4 := newRoom(t, 10, 10)
	mustOK(t, r4.Join("h", 0))
	mustOK(t, r4.Leave("h", 1))
	wantErr(t, r4.Join("z", 2), errClosed)
	_, e := r4.Snapshot(2)
	wantErr(t, e, errClosed)
	_, e = r4.QueuePos("z", 2)
	wantErr(t, e, errClosed)
	wantErr(t, r4.Raise("z", 2), errClosed)
}

// TestQueuePos：名次语义（1 基，删除后重排）。
func TestQueuePos(t *testing.T) {
	r := newRoom(t, 10, 10)
	mustOK(t, r.Join("h", 0))
	for _, u := range []string{"a", "b", "c", "d"} {
		mustOK(t, r.Join(u, 0))
		mustOK(t, r.Raise(u, 0))
	}
	p, err := r.QueuePos("c", 0)
	mustOK(t, err)
	if p != 3 {
		t.Fatalf("c pos = %d, want 3", p)
	}
	mustOK(t, r.Lower("b", 1))
	p, _ = r.QueuePos("c", 1)
	if p != 2 {
		t.Fatalf("c pos = %d after removing b, want 2", p)
	}
	p, _ = r.QueuePos("d", 1)
	if p != 3 {
		t.Fatalf("d pos = %d after removing b, want 3", p)
	}
	_, err = r.QueuePos("b", 1)
	wantErr(t, err, errNotQueued)
	_, err = r.QueuePos("ghost", 1)
	wantErr(t, err, errNoTarget)
	_, err = r.QueuePos("", 1)
	wantErr(t, err, errInvalid)
}
