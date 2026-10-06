package floorcontrol_test

import "testing"

// TestRejectionOrder 逐对覆盖相邻拒绝类别：
// 参数非法 > 时钟回退 > 已关闭 > 操作者不在室内 > 权限不足 >
// 目标不存在 > 状态不允许。
func TestRejectionOrder(t *testing.T) {
	// 参数非法 > 时钟回退（空用户 + 回退 now）。
	r := newRoom(t, 10, 10)
	mustOK(t, r.Join("h", 10))
	wantErr(t, r.Join("", 5), errInvalid)
	wantErr(t, r.Grant("h", -1), errInvalid)

	// 时钟回退 > 已关闭：房间关闭后，回退 now 仍先报回退。
	r1 := newRoom(t, 10, 10)
	mustOK(t, r1.Join("h", 0))
	mustOK(t, r1.Leave("h", 10))
	wantErr(t, r1.Join("z", 9), errClock)

	// 已关闭 > 操作者不在室内。
	wantErr(t, r1.Join("z", 10), errClosed)

	// 操作者不在室内 > 权限不足：未加入者 Grant。
	r2 := newRoom(t, 10, 10)
	mustOK(t, r2.Join("h", 0))
	wantErr(t, r2.Grant("ghost", 1), errNoOperator)

	// 权限不足 > 目标不存在：与会者 Appoint 不存在目标。
	mustOK(t, r2.Join("a", 2))
	wantErr(t, r2.Appoint("a", "ghost", 3), errPerm)

	// 目标不存在 > 状态不允许：主持人 Mute 不存在者。
	wantErr(t, r2.Mute("h", "ghost", 4), errNoTarget)

	// 状态不允许的各情形彼此可区分。
	mustOK(t, r2.Join("b", 5))
	mustOK(t, r2.Raise("b", 6))
	wantErr(t, r2.Raise("b", 6), errInQueue)
	mustOK(t, r2.Mute("h", "b", 7))
	wantErr(t, r2.Raise("b", 7), errMutedRaise)
	wantErr(t, r2.Lower("a", 7), errNotQueued)
	wantErr(t, r2.Yield("a", 7), errNotSpeaking)

	// 队列满。
	r3 := newRoom(t, 10, 1)
	mustOK(t, r3.Join("h", 0))
	mustOK(t, r3.Join("x", 0))
	mustOK(t, r3.Join("y", 0))
	mustOK(t, r3.Raise("x", 0))
	wantErr(t, r3.Raise("y", 0), errFull)

	// 已有发言者 / 队列为空。
	r4 := newRoom(t, 10, 10)
	mustOK(t, r4.Join("h", 0))
	wantErr(t, r4.Grant("h", 0), errEmpty)

	// 已在室 / 角色不变 / 目标为主持人 / 静音状态。
	wantErr(t, r4.Join("h", 1), errAlreadyIn)
	mustOK(t, r4.Join("a", 2))
	mustOK(t, r4.Appoint("h", "a", 3))
	wantErr(t, r4.Appoint("h", "a", 4), errRoleSame)
	wantErr(t, r4.Dismiss("h", "h", 5), errTargetHost)
	mustOK(t, r4.Mute("h", "a", 6))
	wantErr(t, r4.Mute("h", "a", 7), errAlreadyMuted)
	wantErr(t, r4.Unmute("h", "h", 7), errTargetHost)
}
