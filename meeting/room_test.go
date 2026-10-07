package meeting

import "testing"

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected reject: %v", err)
	}
}

func mustReject(t *testing.T, err error, cat Category, reason string) {
	t.Helper()
	rj, ok := err.(*Reject)
	if !ok {
		t.Fatalf("err = %v (%T); want *Reject[%s]: %s", err, err, cat, reason)
	}
	if rj.Cat != cat || rj.Reason != reason {
		t.Fatalf("reject = [%s]: %s; want [%s]: %s", rj.Cat, rj.Reason, cat, reason)
	}
}

func mustSnapshot(t *testing.T, r *Room, now int64) Snapshot {
	t.Helper()
	snap, err := r.Snapshot(now)
	if err != nil {
		t.Fatalf("Snapshot(%d): %v", now, err)
	}
	return snap
}

// joinAll 依次加入成员，全部使用相同时刻。
func joinAll(t *testing.T, r *Room, now int64, users ...string) {
	t.Helper()
	for _, u := range users {
		mustOK(t, r.Join(u, now))
	}
}

// TestNewRoomParams 创建参数校验。
func TestNewRoomParams(t *testing.T) {
	for _, s := range []int64{0, -1, 3601, 1 << 40} {
		if _, err := NewRoom(s, 10); err == nil {
			t.Fatalf("NewRoom(S=%d) should fail", s)
		}
	}
	for _, q := range []int{0, -1, 501} {
		if _, err := NewRoom(10, q); err == nil {
			t.Fatalf("NewRoom(Q=%d) should fail", q)
		}
	}
	if _, err := NewRoom(1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoom(3600, 500); err != nil {
		t.Fatal(err)
	}
}

// TestFirstJoinBecomesHost 首个加入者成为主持人，角色可查。
func TestFirstJoinBecomesHost(t *testing.T) {
	r, _ := NewRoom(10, 3)
	joinAll(t, r, 0, "a", "b", "c")
	snap := mustSnapshot(t, r, 0)
	want := []MemberInfo{
		{User: "a", Role: RoleHost},
		{User: "b", Role: RoleAttendee},
		{User: "c", Role: RoleAttendee},
	}
	if len(snap.Members) != 3 {
		t.Fatalf("members = %v", snap.Members)
	}
	for i := range want {
		if snap.Members[i] != want[i] {
			t.Fatalf("members[%d] = %+v; want %+v", i, snap.Members[i], want[i])
		}
	}
	mustReject(t, r.Join("a", 1), CatState, ReasonAlreadyExists)
}

// TestExpiryExactlyAtDeadline 恰等于到期时刻即视为已到期；差一秒则未到期。
func TestExpiryExactlyAtDeadline(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "spk")
	mustOK(t, r.Raise("spk", 1))
	mustOK(t, r.Grant("host", 2)) // 发言区间 [2, 12)

	snap := mustSnapshot(t, r, 11) // 差一秒到期
	if snap.Speaker != "spk" || snap.RemainingSecs != 1 {
		t.Fatalf("t=11: speaker=%q remain=%d; want spk/1", snap.Speaker, snap.RemainingSecs)
	}
	snap = mustSnapshot(t, r, 12) // 恰等于到期时刻
	if snap.Speaker != "" || snap.RemainingSecs != 0 {
		t.Fatalf("t=12: speaker=%q remain=%d; want none", snap.Speaker, snap.RemainingSecs)
	}
}

// TestCascadeAndGrantTimeAtExpiry 一次操作触发多轮顺延，
// 且顺延时的授予时刻取上一轮的到期时刻，而不是当前操作的 now。
func TestCascadeAndGrantTimeAtExpiry(t *testing.T) {
	r, _ := NewRoom(5, 10)
	joinAll(t, r, 0, "host", "b", "c", "d")
	mustOK(t, r.Raise("b", 0))
	mustOK(t, r.Raise("c", 0))
	mustOK(t, r.Raise("d", 0))
	mustOK(t, r.Grant("host", 0)) // b: [0,5)

	// t=12 的快照触发惰性处理：
	// b 在 5 到期 -> c 在 5 获得（[5,10)）；c 在 10 到期 -> d 在 10 获得（[10,15)）。
	snap := mustSnapshot(t, r, 12)
	if snap.Speaker != "d" {
		t.Fatalf("speaker = %q; want d", snap.Speaker)
	}
	// 若授予时刻被错误地取为 now=12，则剩余为 5-0=5；
	// 正确语义授予时刻为到期时刻 10，剩余 15-12=3。
	if snap.RemainingSecs != 3 {
		t.Fatalf("remaining = %d; want 3 (grant time must be expiry time 10, not now 12)", snap.RemainingSecs)
	}
	if len(snap.Queue) != 0 {
		t.Fatalf("queue = %v; want empty", snap.Queue)
	}

	// 继续推进到 d 也到期：队列已空，发言权空闲。
	snap = mustSnapshot(t, r, 15)
	if snap.Speaker != "" {
		t.Fatalf("speaker = %q; want none", snap.Speaker)
	}
}

// TestMuteSpeaker 静音发言者：立即失去发言权，队首在该操作 now 获得发言权。
func TestMuteSpeaker(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "spk", "next")
	mustOK(t, r.Raise("spk", 0))
	mustOK(t, r.Grant("host", 0)) // spk: [0,10)
	mustOK(t, r.Raise("next", 1))

	mustOK(t, r.Mute("host", "spk", 3))
	snap := mustSnapshot(t, r, 3)
	if snap.Speaker != "next" || snap.RemainingSecs != 10 {
		t.Fatalf("speaker=%q remain=%d; want next/10 (granted at mute time 3)", snap.Speaker, snap.RemainingSecs)
	}
	// 被静音期间不能举手。
	mustReject(t, r.Raise("spk", 4), CatState, ReasonMuted)
	// 静音不因到期而解除。
	snap = mustSnapshot(t, r, 100)
	for _, m := range snap.Members {
		if m.User == "spk" && !m.Muted {
			t.Fatal("mute should not expire")
		}
	}
	// 解除静音后可再次举手。
	mustOK(t, r.Unmute("host", "spk", 101))
	mustOK(t, r.Raise("spk", 102))
}

// TestMuteQueuedMember 静音队列成员：被移出队列，名次随之变化。
func TestMuteQueuedMember(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "a", "b", "c")
	mustOK(t, r.Raise("a", 0))
	mustOK(t, r.Raise("b", 0))
	mustOK(t, r.Raise("c", 0))
	if pos, _ := r.QueuePos("c"); pos != 3 {
		t.Fatalf("pos(c) = %d; want 3", pos)
	}
	mustOK(t, r.Mute("host", "b", 1))
	if _, err := r.QueuePos("b"); err == nil {
		t.Fatal("muted member must be removed from queue")
	} else {
		mustReject(t, err, CatState, ReasonNotInQueue)
	}
	if pos, _ := r.QueuePos("c"); pos != 2 {
		t.Fatalf("pos(c) = %d; want 2 after b removed", pos)
	}
	snap := mustSnapshot(t, r, 1)
	if len(snap.Queue) != 2 || snap.Queue[0] != "a" || snap.Queue[1] != "c" {
		t.Fatalf("queue = %v; want [a c]", snap.Queue)
	}
	// 重复静音与解除未静音者。
	mustReject(t, r.Mute("host", "b", 2), CatState, ReasonAlreadyMuted)
	mustReject(t, r.Unmute("host", "a", 2), CatState, ReasonNotMuted)
}

// TestMuteHostRejected 不能静音主持人本人。
func TestMuteHostRejected(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "a")
	mustReject(t, r.Mute("host", "host", 1), CatState, ReasonMuteHost)
	// 协管员无权静音。
	mustOK(t, r.Appoint("host", "a", 1))
	mustReject(t, r.Mute("a", "host", 2), CatPermission, ReasonNeedHost)
}

// TestHostTransferPriorities 主持人移交的三个优先级：
// 加入最早的协管员 > 加入最早的其余成员 > 无其他成员则关闭。
func TestHostTransferPriorities(t *testing.T) {
	// 第一优先：加入最早的协管员（不是任命最早的）。
	r1, _ := NewRoom(10, 5)
	joinAll(t, r1, 0, "host", "x", "y", "z")
	mustOK(t, r1.Appoint("host", "z", 1)) // z 先被任命
	mustOK(t, r1.Appoint("host", "x", 2)) // x 后加入但先加入会议室
	mustOK(t, r1.Leave("host", 3))
	snap := mustSnapshot(t, r1, 3)
	for _, m := range snap.Members {
		if m.User == "x" && m.Role != RoleHost {
			t.Fatalf("x should be host (earliest joined cohost), got %v", m.Role)
		}
		if m.User == "z" && m.Role != RoleCoHost {
			t.Fatalf("z should stay cohost, got %v", m.Role)
		}
	}

	// 第二优先：没有协管员时加入最早的成员。
	r2, _ := NewRoom(10, 5)
	joinAll(t, r2, 0, "host", "m1", "m2")
	mustOK(t, r2.Leave("host", 1))
	snap = mustSnapshot(t, r2, 1)
	for _, m := range snap.Members {
		if m.User == "m1" && m.Role != RoleHost {
			t.Fatalf("m1 should be host, got %v", m.Role)
		}
	}

	// 第三：无其他成员则关闭。
	r3, _ := NewRoom(10, 5)
	joinAll(t, r3, 0, "host")
	mustOK(t, r3.Leave("host", 1))
	mustReject(t, r3.Join("new", 2), CatClosed, ReasonRoomClosed)
	mustReject(t, r3.Raise("new", 2), CatClosed, ReasonRoomClosed)
	if _, err := r3.Snapshot(2); err == nil {
		t.Fatal("snapshot on closed room should fail")
	} else {
		mustReject(t, err, CatClosed, ReasonRoomClosed)
	}
	if _, err := r3.QueuePos("new"); err == nil {
		t.Fatal("queuepos on closed room should fail")
	} else {
		mustReject(t, err, CatClosed, ReasonRoomClosed)
	}
}

// TestHostTransferKeepsFloorAndQueue 移交不改变发言权与队列。
func TestHostTransferKeepsFloorAndQueue(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "co", "spk", "q1")
	mustOK(t, r.Appoint("host", "co", 0))
	mustOK(t, r.Raise("spk", 0))
	mustOK(t, r.Grant("host", 0)) // spk: [0,10)
	mustOK(t, r.Raise("q1", 1))
	mustOK(t, r.Leave("host", 2)) // co 接任主持人

	snap := mustSnapshot(t, r, 2)
	if snap.Speaker != "spk" || snap.RemainingSecs != 8 {
		t.Fatalf("speaker=%q remain=%d; want spk/8 (transfer must not touch floor)", snap.Speaker, snap.RemainingSecs)
	}
	if len(snap.Queue) != 1 || snap.Queue[0] != "q1" {
		t.Fatalf("queue = %v; want [q1]", snap.Queue)
	}
	// 新主持人可行使主持人权限。
	mustOK(t, r.Mute("co", "q1", 3))
}

// TestSpeakerLeaveTriggersSuccession 发言者离开等同于失去发言权并触发顺延。
func TestSpeakerLeaveTriggersSuccession(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "spk", "next")
	mustOK(t, r.Raise("spk", 0))
	mustOK(t, r.Grant("host", 0))
	mustOK(t, r.Raise("next", 1))
	mustOK(t, r.Leave("spk", 4))
	snap := mustSnapshot(t, r, 4)
	if snap.Speaker != "next" || snap.RemainingSecs != 10 {
		t.Fatalf("speaker=%q remain=%d; want next/10 (granted at leave time 4)", snap.Speaker, snap.RemainingSecs)
	}
}

// TestQueuedMemberLeave 队列成员离开被移出队列。
func TestQueuedMemberLeave(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "a", "b")
	mustOK(t, r.Raise("a", 0))
	mustOK(t, r.Raise("b", 0))
	mustOK(t, r.Leave("a", 1))
	snap := mustSnapshot(t, r, 1)
	if len(snap.Queue) != 1 || snap.Queue[0] != "b" {
		t.Fatalf("queue = %v; want [b]", snap.Queue)
	}
}

// TestRejectOrderAdjacentPairs 拒绝次序：同时违反相邻两个类别时，
// 只报告优先级更高的那个。覆盖全部六对相邻类别。
func TestRejectOrderAdjacentPairs(t *testing.T) {
	setup := func(t *testing.T) *Room {
		r, _ := NewRoom(10, 2)
		joinAll(t, r, 0, "host", "co", "att")
		mustOK(t, r.Appoint("host", "co", 0))
		return r
	}

	t.Run("param>clock", func(t *testing.T) {
		r := setup(t)
		mustOK(t, r.Raise("att", 5)) // 时钟推进到 5
		// 空用户（参数非法）且 now=1 时钟回退：报参数非法。
		mustReject(t, r.Join("", 1), CatInvalidParam, ReasonEmptyUser)
	})

	t.Run("clock>closed", func(t *testing.T) {
		r, _ := NewRoom(10, 2)
		joinAll(t, r, 5, "host")
		mustOK(t, r.Leave("host", 6)) // 关闭，时钟为 6
		mustReject(t, r.Join("x", 3), CatClockRegression, ReasonClockBackwards)
	})

	t.Run("closed>not_in_room", func(t *testing.T) {
		r, _ := NewRoom(10, 2)
		joinAll(t, r, 0, "host")
		mustOK(t, r.Leave("host", 1))
		// ghost 不在室内且房间已关闭：报已关闭。
		mustReject(t, r.Raise("ghost", 2), CatClosed, ReasonRoomClosed)
	})

	t.Run("not_in_room>permission", func(t *testing.T) {
		r := setup(t)
		// ghost 不在室内；Grant 需要权限：报不在室内。
		mustReject(t, r.Grant("ghost", 1), CatNotInRoom, ReasonNotInRoom)
	})

	t.Run("permission>target_not_found", func(t *testing.T) {
		r := setup(t)
		// att 无权静音，且目标 ghost 不存在：报权限不足。
		mustReject(t, r.Mute("att", "ghost", 1), CatPermission, ReasonNeedHost)
	})

	t.Run("target_not_found>state", func(t *testing.T) {
		r := setup(t)
		// 主持人撤销 ghost：目标不存在（若先查状态会报 not_cohost）。
		mustReject(t, r.Revoke("host", "ghost", 1), CatTargetNotFound, ReasonTargetNotFound)
	})
}

// TestClockAdvanceOnRejectedOps 通过参数与时钟检查的操作，即使最终被拒，
// 也会先做惰性到期处理并推进时钟；参数非法或时钟回退被拒的操作不做任何处理。
func TestClockAdvanceOnRejectedOps(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "spk", "att")
	mustOK(t, r.Raise("spk", 0))
	mustOK(t, r.Grant("host", 0)) // spk: [0,10)

	// att 无权限 Grant（被拒），但时钟推进到 20，惰性处理生效：spk 在 10 到期。
	mustReject(t, r.Grant("att", 20), CatPermission, ReasonNeedHostOrCo)
	snap := mustSnapshot(t, r, 20)
	if snap.Speaker != "" {
		t.Fatalf("speaker = %q; want none (expiry processed by rejected op)", snap.Speaker)
	}
	// 时钟已推进到 20：now=15 报时钟回退。
	mustReject(t, r.Raise("att", 15), CatClockRegression, ReasonClockBackwards)

	// 参数非法的操作不推进时钟：now=25 合法但被拒（空用户），时钟保持 20。
	mustReject(t, r.Join("", 25), CatInvalidParam, ReasonEmptyUser)
	mustOK(t, r.Raise("att", 21)) // 21 >= 20，未被 25 污染

	// 时钟回退被拒的操作不推进时钟。
	mustReject(t, r.Raise("att", 19), CatClockRegression, ReasonClockBackwards)
	mustOK(t, r.Raise("host", 22))
}

// TestRaiseRejectionsDistinguishable 重复举手、已在发言、被静音、
// 队列满四种拒绝可区分。
func TestRaiseRejectionsDistinguishable(t *testing.T) {
	r, _ := NewRoom(10, 2)
	joinAll(t, r, 0, "host", "spk", "q1", "q2", "muted", "late")
	mustOK(t, r.Raise("spk", 0))
	mustOK(t, r.Grant("host", 0))
	mustReject(t, r.Raise("spk", 1), CatState, ReasonAlreadySpeaking)

	mustOK(t, r.Raise("q1", 1))
	mustReject(t, r.Raise("q1", 1), CatState, ReasonAlreadyInQueue)

	mustOK(t, r.Mute("host", "muted", 1))
	mustReject(t, r.Raise("muted", 1), CatState, ReasonMuted)

	mustOK(t, r.Raise("q2", 1)) // 队列达到容量 2
	mustReject(t, r.Raise("late", 1), CatState, ReasonQueueFull)
}

// TestGrantAndYield Grant 的拒绝与 Yield 的语义。
func TestGrantAndYield(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "co", "a", "b")
	mustOK(t, r.Appoint("host", "co", 0))

	// 队列为空时拒绝。
	mustReject(t, r.Grant("host", 0), CatState, ReasonQueueEmpty)

	mustOK(t, r.Raise("a", 0))
	mustOK(t, r.Grant("co", 1)) // 协管员也可授予
	// 已有发言者时拒绝。
	mustOK(t, r.Raise("b", 1))
	mustReject(t, r.Grant("host", 2), CatState, ReasonSpeakerExists)

	// 非发言者不能 Yield。
	mustReject(t, r.Yield("b", 2), CatState, ReasonNotSpeaker)
	// 发言者 Yield 后发言权空闲，不自动顺延。
	mustOK(t, r.Yield("a", 3))
	snap := mustSnapshot(t, r, 3)
	if snap.Speaker != "" {
		t.Fatalf("speaker = %q; want none after yield", snap.Speaker)
	}
	if len(snap.Queue) != 1 || snap.Queue[0] != "b" {
		t.Fatalf("queue = %v; want [b] (yield does not auto-grant)", snap.Queue)
	}
	// 主持人再次手动授予。
	mustOK(t, r.Grant("host", 4))
	snap = mustSnapshot(t, r, 4)
	if snap.Speaker != "b" || snap.RemainingSecs != 10 {
		t.Fatalf("speaker=%q remain=%d; want b/10", snap.Speaker, snap.RemainingSecs)
	}
}

// TestAppointRevoke 任命与撤销协管员的权限与状态检查。
func TestAppointRevoke(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "co", "att")
	mustReject(t, r.Appoint("att", "att", 0), CatPermission, ReasonNeedHostOrCo)
	mustOK(t, r.Appoint("host", "co", 0))
	// 协管员可继续任命。
	mustOK(t, r.Appoint("co", "att", 1))
	mustReject(t, r.Appoint("host", "att", 2), CatState, ReasonAlreadyCoHost)
	mustReject(t, r.Appoint("host", "host", 2), CatState, ReasonTargetIsHost)
	// 撤销仅主持人可以。
	mustReject(t, r.Revoke("co", "att", 2), CatPermission, ReasonNeedHost)
	mustOK(t, r.Revoke("host", "att", 3))
	mustReject(t, r.Revoke("host", "att", 4), CatState, ReasonNotCoHost)
}

// TestQueuePosAndLower 名次查询与取消举手。
func TestQueuePosAndLower(t *testing.T) {
	r, _ := NewRoom(10, 5)
	joinAll(t, r, 0, "host", "a", "b", "c")
	mustOK(t, r.Raise("a", 0))
	mustOK(t, r.Raise("b", 0))
	mustOK(t, r.Raise("c", 0))
	for i, u := range []string{"a", "b", "c"} {
		if pos, err := r.QueuePos(u); err != nil || pos != i+1 {
			t.Fatalf("pos(%s) = %d,%v; want %d", u, pos, err, i+1)
		}
	}
	mustOK(t, r.Lower("b", 1))
	if pos, _ := r.QueuePos("c"); pos != 2 {
		t.Fatalf("pos(c) = %d; want 2", pos)
	}
	mustReject(t, r.Lower("b", 2), CatState, ReasonNotInQueue)
	if _, err := r.QueuePos("ghost"); err == nil {
		t.Fatal("ghost should not have position")
	} else {
		mustReject(t, err, CatNotInRoom, ReasonNotInRoom)
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		r, _ := NewRoom(7, 3)
		var out []string
		record := func(err error) {
			if err == nil {
				out = append(out, "ok")
			} else {
				out = append(out, err.Error())
			}
		}
		record(r.Join("h", 0))
		record(r.Join("a", 0))
		record(r.Join("b", 1))
		record(r.Raise("a", 1))
		record(r.Raise("b", 2))
		record(r.Grant("h", 3))
		record(r.Raise("h", 4))
		record(r.Mute("h", "b", 5))
		record(r.Leave("h", 20))
		snap, err := r.Snapshot(20)
		record(err)
		out = append(out, snap.Speaker)
		out = append(out, snap.Queue...)
		for _, m := range snap.Members {
			out = append(out, m.User, m.Role.String())
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatal("replay mismatch")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay mismatch at %d: %q vs %q", i, first[i], second[i])
		}
	}
}

// TestCascadeEndsWithEmptyQueue 多轮顺延把队列耗尽后，发言权在到期时刻空闲。
func TestCascadeEndsWithEmptyQueue(t *testing.T) {
	r, _ := NewRoom(5, 10)
	joinAll(t, r, 0, "host", "b", "c")
	mustOK(t, r.Raise("b", 0))
	mustOK(t, r.Raise("c", 0))
	mustOK(t, r.Grant("host", 0)) // b: [0,5)

	snap := mustSnapshot(t, r, 100) // b@5->c, c@10->空
	if snap.Speaker != "" {
		t.Fatalf("speaker = %q; want none", snap.Speaker)
	}
	// 之后主持人可重新 Grant（先举手）。
	mustOK(t, r.Raise("b", 100))
	mustOK(t, r.Grant("host", 101)) // 手动 Grant 的授予时刻取操作 now
	snap = mustSnapshot(t, r, 101)
	if snap.Speaker != "b" || snap.RemainingSecs != 5 {
		t.Fatalf("speaker=%q remain=%d; want b/5", snap.Speaker, snap.RemainingSecs)
	}
}

// TestExpiryRoundsProbe 惰性到期处理的开销只与实际到期轮数有关：
// 成员再多，没有到期就没有处理；一次操作恰好触发 k 轮。
func TestExpiryRoundsProbe(t *testing.T) {
	r, _ := NewRoom(5, 500)
	users := []string{"host", "spk", "b1", "b2", "b3"}
	for i := 0; i < 495; i++ {
		users = append(users, string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+string(rune('0'+i%10)))
	}
	joinAll(t, r, 0, users...) // 500 名成员
	mustOK(t, r.Raise("spk", 0))
	mustOK(t, r.Grant("host", 0)) // [0,5)

	before := r.expiryRounds
	mustSnapshot(t, r, 4) // 未到期
	if r.expiryRounds != before {
		t.Fatalf("no expiry should happen: rounds %d -> %d", before, r.expiryRounds)
	}
	mustSnapshot(t, r, 5) // 恰好一轮到期，队列空 -> 发言权空闲
	if r.expiryRounds != before+1 {
		t.Fatalf("rounds = %d; want %d", r.expiryRounds, before+1)
	}

	// 构造连续 3 轮顺延。
	mustOK(t, r.Raise("b1", 5))
	mustOK(t, r.Raise("b2", 5))
	mustOK(t, r.Raise("b3", 5))
	mustOK(t, r.Grant("host", 5)) // b1: [5,10)
	before = r.expiryRounds
	snap := mustSnapshot(t, r, 22) // b1@10->b2, b2@15->b3, b3@20->空
	if r.expiryRounds != before+3 {
		t.Fatalf("rounds = %d; want %d (3 cascades)", r.expiryRounds, before+3)
	}
	if snap.Speaker != "" {
		t.Fatalf("speaker = %q; want none", snap.Speaker)
	}
}
