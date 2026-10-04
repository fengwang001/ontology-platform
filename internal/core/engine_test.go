package core

import (
	"errors"
	"testing"
)

func queueOf(r *Room) []int64 {
	var q []int64
	for n := r.head.next; n != r.tail; n = n.next {
		q = append(q, n.user)
	}
	return q
}

func slotsOf(r *Room) []int64 {
	out := make([]int64, len(r.slots))
	for i, u := range r.slots {
		out[i] = u
	}
	return out
}

func mustOK(t *testing.T, name string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 期望成功，实际 %v", name, err)
	}
}

func wantErr(t *testing.T, name string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: 期望 %v，实际 %v", name, want, got)
	}
}

// 规格示例一：入口补麦、禁言者跳过、到期取等。
func TestSpecExampleFillAndExpiry(t *testing.T) {
	const o, a, x, y, z = 1, 2, 3, 4, 5
	r := New(1)
	for _, u := range []int64{o, a, x, y, z} {
		mustOK(t, "join", r.Join(0, u))
	}
	mustOK(t, "set a admin", r.SetRole(0, o, a, Admin))
	for _, u := range []int64{x, y, z} {
		mustOK(t, "take", r.TakeMic(0, u))
	}
	if got := slotsOf(r); got[0] != x {
		t.Fatalf("x 应在麦: %v", got)
	}
	if got := queueOf(r); len(got) != 2 || got[0] != y || got[1] != z {
		t.Fatalf("队列应为 [y z]: %v", got)
	}
	mustOK(t, "mute y", r.Mute(10, a, y, 100))
	mustOK(t, "drop x", r.DropMic(20, x))
	if slotsOf(r)[0] != z {
		t.Fatalf("跳过禁言的 y，z 应上麦: %v", slotsOf(r))
	}
	if got := queueOf(r); len(got) != 1 || got[0] != y {
		t.Fatalf("队列应为 [y]: %v", got)
	}
	mustOK(t, "drop z", r.DropMic(30, z))
	if slotsOf(r)[0] != 0 {
		t.Fatalf("y 仍禁言，麦应空: %v", slotsOf(r))
	}
	// now=99：禁言仍生效，入口补麦跳过 y，x 直接上麦。
	mustOK(t, "take x@99", r.TakeMic(99, x))
	if slotsOf(r)[0] != x {
		t.Fatalf("99ms 时 x 应上麦: %v", slotsOf(r))
	}
	if got := queueOf(r); len(got) != 1 || got[0] != y {
		t.Fatalf("y 仍在队列: %v", got)
	}
}

// 规格示例一（续）：now=100 禁言恰好解除，y 入口补麦上麦，x 排队；
// 不在房者发起时入口补麦随拒绝一并撤销。
func TestSpecExpiryBoundaryAndRollback(t *testing.T) {
	const o, a, x, y, z = 1, 2, 3, 4, 5
	r := New(1)
	for _, u := range []int64{o, a, x, y, z} {
		mustOK(t, "join", r.Join(0, u))
	}
	mustOK(t, "set a admin", r.SetRole(0, o, a, Admin))
	for _, u := range []int64{x, y, z} {
		mustOK(t, "take", r.TakeMic(0, u))
	}
	mustOK(t, "mute y", r.Mute(10, a, y, 100))
	mustOK(t, "drop x", r.DropMic(20, x))
	mustOK(t, "drop z", r.DropMic(30, z))
	// 麦空、队列 [y(禁言至100)]。100ms 时陌生人发起：入口补麦先让 y 上麦，
	// 随后 actor 不在房 -> 拒绝，入口补麦必须撤销。
	wantErr(t, "stranger take rolled back", r.TakeMic(100, 9), ErrNotInRoom)
	if slotsOf(r)[0] != 0 {
		t.Fatalf("拒绝须撤销入口补麦，麦应空: %v", slotsOf(r))
	}
	if got := queueOf(r); len(got) != 1 || got[0] != y {
		t.Fatalf("y 须回到队列原位: %v", got)
	}
	if r.maxNow != 30 {
		t.Fatalf("被拒操作不推进时钟: maxNow=%d", r.maxNow)
	}
	// 合法操作：入口补麦 y 上麦，x 排队。
	mustOK(t, "take x@100", r.TakeMic(100, x))
	if slotsOf(r)[0] != y {
		t.Fatalf("100ms 禁言到期，y 应入口补麦上麦: %v", slotsOf(r))
	}
	if got := queueOf(r); len(got) != 1 || got[0] != x {
		t.Fatalf("x 应排队，无法插队: %v", got)
	}
}

// 规格示例二：同级可覆盖/解禁、高级压制、缩短、到期后重新施加、Owner 不可禁言。
func TestSpecExampleSuppression(t *testing.T) {
	const o, a, a2, y = 1, 2, 6, 4
	r := New(4)
	for _, u := range []int64{o, a, a2, y} {
		mustOK(t, "join", r.Join(0, u))
	}
	mustOK(t, "a admin", r.SetRole(0, o, a, Admin))
	mustOK(t, "a2 admin", r.SetRole(0, o, a2, Admin))
	mustOK(t, "mute a->y@100", r.Mute(10, a, y, 100))
	if r.mutes[y].level != Admin || r.mutes[y].until != 100 {
		t.Fatalf("L0 应为 2、until=100: %+v", r.mutes[y])
	}
	mustOK(t, "unmute a2 same level", r.Unmute(15, a2, y))
	mustOK(t, "re-mute", r.Mute(16, a, y, 100))
	mustOK(t, "owner shorten", r.Mute(20, o, y, 50))
	if r.mutes[y].until != 50 || r.mutes[y].level != Owner {
		t.Fatalf("until 应缩短为 50 且 L0=3: %+v", r.mutes[y])
	}
	wantErr(t, "admin suppressed", r.Mute(30, a, y, 200), ErrSuppressed)
	if r.mutes[y].until != 50 {
		t.Fatalf("被拒后 until 仍应为 50（不允许延长）: %+v", r.mutes[y])
	}
	mustOK(t, "mute at expiry boundary", r.Mute(50, a, y, 200))
	if r.mutes[y].until != 200 || r.mutes[y].level != Admin {
		t.Fatalf("到期边界可重新禁言且 L0=2: %+v", r.mutes[y])
	}
	wantErr(t, "admin mute admin", r.Mute(51, a, a2, 300), ErrLowLevel)
	wantErr(t, "admin mute owner", r.Mute(51, a, o, 300), ErrLowLevel)
	wantErr(t, "self mute level", r.Mute(51, o, o, 300), ErrLowLevel)
	mustOK(t, "clear", r.Unmute(60, o, y))
	wantErr(t, "unmute not muted", r.Unmute(61, o, y), ErrNotMuted)
}

// L0 是施加时快照：施加者降级不改变既有压制力。
// Owner 施加 L0=3 后移交降为 Admin，此刻再覆盖同一禁言即被自己留下的 L0 压制。
func TestL0SnapshotOnDemotion(t *testing.T) {
	const o, a, y = 1, 2, 4
	r := New(2)
	for _, u := range []int64{o, a, y} {
		mustOK(t, "join", r.Join(0, u))
	}
	mustOK(t, "owner mute y", r.Mute(10, o, y, 100))
	mustOK(t, "transfer owner->a", r.Transfer(20, o, a))
	if r.roles[o] != Admin {
		t.Fatalf("移交后 o 应降为 Admin: %v", r.roles)
	}
	// 现在 o 为 Admin(2) > y(1)，等级检查通过，但 L0=3 快照仍压制。
	wantErr(t, "demoted owner self-suppressed", r.Mute(30, o, y, 200), ErrSuppressed)
	if r.mutes[y].level != Owner || r.mutes[y].until != 100 {
		t.Fatalf("被拒后 L0/until 保持快照值: %+v", r.mutes[y])
	}
}

// 离开再加入不洗禁言。
func TestMuteSurvivesLeaveRejoin(t *testing.T) {
	const o, a, y = 1, 2, 4
	r := New(1)
	for _, u := range []int64{o, a, y} {
		mustOK(t, "join", r.Join(0, u))
	}
	mustOK(t, "a admin", r.SetRole(0, o, a, Admin))
	mustOK(t, "take y", r.TakeMic(0, y))
	mustOK(t, "mute y", r.Mute(10, a, y, 100))
	if slotsOf(r)[0] != 0 {
		t.Fatalf("禁言应立即下麦: %v", slotsOf(r))
	}
	mustOK(t, "y leave", r.Leave(20, y))
	mustOK(t, "y rejoin", r.Join(30, y))
	wantErr(t, "muted after rejoin", r.TakeMic(40, y), ErrMuted)
	if !r.muted(y, 99) || r.muted(y, 100) {
		t.Fatalf("禁言在 99 生效、100 恰好解除")
	}
}

// 被禁言的排队者保留原位置，补麦只跳过不移除。
func TestMutedQueuedKeepsPosition(t *testing.T) {
	const o, a, p, q, y = 1, 2, 3, 4, 5
	r := New(1)
	for _, u := range []int64{o, a, p, q, y} {
		mustOK(t, "join", r.Join(0, u))
	}
	mustOK(t, "a admin", r.SetRole(0, o, a, Admin))
	mustOK(t, "p on mic", r.TakeMic(0, p))
	mustOK(t, "y queue", r.TakeMic(0, y))
	mustOK(t, "q queue", r.TakeMic(0, q))
	mustOK(t, "mute y", r.Mute(10, a, y, 100))
	if got := queueOf(r); len(got) != 2 || got[0] != y || got[1] != q {
		t.Fatalf("禁言排队者保留原位: %v", got)
	}
	mustOK(t, "p drop", r.DropMic(20, p))
	if slotsOf(r)[0] != q {
		t.Fatalf("应跳过 y 让 q 上麦: %v", slotsOf(r))
	}
	if got := queueOf(r); len(got) != 1 || got[0] != y {
		t.Fatalf("y 仍留在队首: %v", got)
	}
	mustOK(t, "drop q@100", r.DropMic(100, q))
	if slotsOf(r)[0] != y {
		t.Fatalf("到期后入口补麦 y 上麦: %v", slotsOf(r))
	}
}

// Owner 移交与离开规则。
func TestTransferAndOwnerLeave(t *testing.T) {
	const o, x, y = 1, 3, 4
	r := New(2)
	for _, u := range []int64{o, x, y} {
		mustOK(t, "join", r.Join(0, u))
	}
	wantErr(t, "member transfer", r.Transfer(10, x, y), ErrLowLevel)
	wantErr(t, "transfer to self", r.Transfer(10, o, o), ErrBadArgument)
	wantErr(t, "transfer to stranger", r.Transfer(10, o, 9), ErrNoTarget)
	mustOK(t, "transfer", r.Transfer(20, o, x))
	if r.roles[x] != Owner || r.roles[o] != Admin {
		t.Fatalf("移交后 x=Owner、o=Admin: %v", r.roles)
	}
	wantErr(t, "owner leave must transfer", r.Leave(30, x), ErrMustTransfer)
	mustOK(t, "o leave", r.Leave(40, o))
	mustOK(t, "y leave", r.Leave(50, y))
	mustOK(t, "last owner leave", r.Leave(60, x))
	if len(r.roles) != 0 {
		t.Fatalf("房间应清空: %v", r.roles)
	}
	mustOK(t, "y join empty", r.Join(70, y))
	if r.roles[y] != Owner {
		t.Fatalf("空房首个加入者应为 Owner: %v", r.roles)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > by 不在房 > target 不在房 >
// 等级不足 > 被压制 > 其余状态类。
func TestRejectionOrder(t *testing.T) {
	const o, a, y = 1, 2, 4
	r := New(1)
	for _, u := range []int64{o, a, y} {
		mustOK(t, "join", r.Join(100, u))
	}
	mustOK(t, "a admin", r.SetRole(100, o, a, Admin))
	mustOK(t, "mute y", r.Mute(100, a, y, 200))
	wantErr(t, "bad arg beats all", r.Mute(90, 9, 9, 50), ErrBadArgument)
	wantErr(t, "rewind beats absent by", r.Mute(90, 9, y, 300), ErrClockRewind)
	wantErr(t, "absent by beats absent target", r.Mute(110, 9, 8, 300), ErrNotInRoom)
	wantErr(t, "absent target beats level", r.Mute(110, y, 8, 300), ErrNoTarget)
	wantErr(t, "level beats suppressed (mute)", r.Mute(110, y, y, 300), ErrLowLevel)
	wantErr(t, "level beats suppressed (unmute)", r.Unmute(110, y, y), ErrLowLevel)
	mustOK(t, "owner mute y@300", r.Mute(120, o, y, 300))
	wantErr(t, "suppressed beats not-muted", r.Unmute(130, a, y), ErrSuppressed)
	wantErr(t, "muted beats duplicate", r.TakeMic(140, y), ErrMuted)
	wantErr(t, "not in mic", r.DropMic(150, y), ErrNotInMic)
}

// 重复加入、任免等级边界、时钟与参数边界。
func TestJoinSetRoleAndClock(t *testing.T) {
	r := New(2)
	wantErr(t, "empty room actor", r.Leave(0, 1), ErrNotInRoom)
	mustOK(t, "join first owner", r.Join(0, 1))
	wantErr(t, "duplicate join", r.Join(1, 1), ErrDuplicate)
	mustOK(t, "join member", r.Join(1, 2))
	mustOK(t, "promote to admin", r.SetRole(2, 1, 2, Admin))
	wantErr(t, "admin set admin", r.SetRole(3, 2, 2, Admin), ErrLowLevel)
	wantErr(t, "owner to owner", r.SetRole(3, 1, 2, Owner), ErrBadArgument)
	mustOK(t, "join member3", r.Join(3, 3))
	mustOK(t, "admin keep member", r.SetRole(4, 2, 3, Member))
	wantErr(t, "admin promote admin", r.SetRole(4, 2, 3, Admin), ErrLowLevel)
	wantErr(t, "negative now", r.Join(-1, 9), ErrBadArgument)
	wantErr(t, "too large now", r.Join(1_000_000_000_001, 9), ErrBadArgument)
	wantErr(t, "non-positive user", r.Join(5, 0), ErrBadArgument)
	wantErr(t, "max now ok", r.Join(1_000_000_000_000, 4), nil)
	wantErr(t, "rewind after max", r.Join(4, 5), ErrClockRewind)
}

// touched 证明：从队列中部移除一人触碰节点数 ≤ 3，100 与 10000 两档对照。
func TestTouchedMiddleRemovalIndependentOfLength(t *testing.T) {
	for _, n := range []int{100, 10000} {
		r := New(1)
		mustOK(t, "join owner", r.Join(0, 1))
		mustOK(t, "owner take", r.TakeMic(0, 1))
		for i := 2; i <= n+1; i++ {
			mustOK(t, "join", r.Join(0, int64(i)))
			mustOK(t, "queue", r.TakeMic(0, int64(i)))
		}
		target := int64(n/2 + 1)
		// 队列其余成员全部禁言，使入口/出口补麦不挪动队列，touched 只反映中部摘除。
		for i := 2; i <= n+1; i++ {
			if int64(i) == target {
				continue
			}
			if err := r.Mute(10, 1, int64(i), 1000); err != nil {
				t.Fatalf("n=%d mute %d: %v", n, i, err)
			}
		}
		r.touched = 0
		if err := r.DropMic(20, target); err != nil {
			t.Fatalf("n=%d drop middle: %v", n, err)
		}
		// 入口补麦与出口补麦都只能扫描/跳过禁言节点——它们也属于被跳过的禁言者；
		// 但中部摘除本身的链表触碰恰为 3，单独以白盒摘除复核。
		r2 := New(1)
		for i := 0; i < n; i++ {
			r2.enqueue(int64(1000 + i))
		}
		r2.touched = 0
		mid := r2.head.next
		for i := 0; i < n/2; i++ {
			mid = mid.next
		}
		r2.removeNode(mid)
		if r2.touched != 3 {
			t.Fatalf("n=%d 中部摘除触碰节点数应为 3，实际 %d", n, r2.touched)
		}
	}
}

// 补麦扫描项数 ≤ 上麦人数 + 跳过禁言者数 + 1。
func TestFillScanBound(t *testing.T) {
	const M = 3
	r := New(M)
	mustOK(t, "join owner", r.Join(0, 1))
	users := []int64{1}
	for i := int64(2); i <= 12; i++ {
		mustOK(t, "join", r.Join(0, i))
		users = append(users, i)
	}
	// 3 人占麦，9 人排队；队列中前 5 人禁言、后 4 人未禁言。
	for i := 0; i < M; i++ {
		mustOK(t, "take", r.TakeMic(0, users[i]))
	}
	for _, u := range users[M:] {
		mustOK(t, "queue", r.TakeMic(0, u))
	}
	for _, u := range users[M : M+5] {
		mustOK(t, "mute queued", r.Mute(10, 1, u, 1000))
	}
	// 麦上 3 人全部离开麦位后触发一次补麦：跳过 5 个禁言者，上 3 个未禁言者，
	// 扫描在第 8 项（5 跳过 + 3 上麦）后因空麦填满停止，≤ 3+5+1。
	for i := range r.slots {
		r.slots[i] = 0
	}
	r.scanned = 0
	r.fill(20)
	if r.scanned > M+5+1 {
		t.Fatalf("扫描项数 %d 超过上界 %d", r.scanned, M+5+1)
	}
	if r.scanned != M+5+1 {
		t.Fatalf("本场景应恰扫描 %d 项，实际 %d", M+5+1, r.scanned)
	}
	free := 0
	for _, s := range r.slots {
		if s == 0 {
			free++
		}
	}
	if free != 0 {
		t.Fatalf("3 个空麦应全部补齐，剩余空麦 %d", free)
	}
}
