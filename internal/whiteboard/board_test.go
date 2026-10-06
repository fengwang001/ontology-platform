package whiteboard

import (
	"errors"
	"strings"
	"testing"
)

func mustAdd(t *testing.T, b *Board, user, id string, now int64) {
	t.Helper()
	if err := b.Add(user, id, now); err != nil {
		t.Fatalf("Add(%s,%s,%d): %v", user, id, now, err)
	}
}

func assertOrder(t *testing.T, b *Board, want []string) {
	t.Helper()
	got := b.Order()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order: got %v want %v", got, want)
	}
}

func kindOf(t *testing.T, err error) RejectKind {
	t.Helper()
	var re *RejectError
	if errors.As(err, &re) {
		return re.Kind
	}
	var le *LockError
	if errors.As(err, &le) {
		return KindLocked
	}
	t.Fatalf("unexpected error type: %v", err)
	return 0
}

func assertKind(t *testing.T, err error, want RejectKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected reject %s, got success", want)
	}
	if got := kindOf(t, err); got != want {
		t.Fatalf("reject kind: got %s want %s (%v)", got, want, err)
	}
}

func TestAddOrderRankBetween(t *testing.T) {
	b := New()
	mustAdd(t, b, "u1", "a", 10)
	mustAdd(t, b, "u2", "b", 11)
	mustAdd(t, b, "u3", "c", 12)
	assertOrder(t, b, []string{"a", "b", "c"})
	if b.Rev() != 3 {
		t.Fatalf("rev=%d", b.Rev())
	}
	if r, _ := b.Rank("a"); r != 1 {
		t.Fatalf("rank a=%d", r)
	}
	if r, _ := b.Rank("c"); r != 3 {
		t.Fatalf("rank c=%d", r)
	}
	got, err := b.Between(2, 3)
	if err != nil || strings.Join(got, ",") != "b,c" {
		t.Fatalf("between: %v %v", got, err)
	}
	if _, err := b.Between(1, 4); err == nil {
		t.Fatalf("between out of range must fail")
	}
	if _, err := b.Rank("missing"); err == nil {
		t.Fatalf("rank missing must fail")
	}
}

func TestClockMonotonicAndZeroSideEffect(t *testing.T) {
	b := New()
	mustAdd(t, b, "u", "a", 10)
	before := b.Snapshot(10)
	assertKind(t, b.Add("u", "b", 9), KindClockBackward)
	after := b.Snapshot(10)
	if snapshotsEqual(before, after) != "" {
		t.Fatalf("rejected op changed state: %s", snapshotsEqual(before, after))
	}
	mustAdd(t, b, "u", "b", 10) // 相等时刻允许
	assertKind(t, b.Add("u", "c", -1), KindInvalidArgument)
	assertKind(t, b.Add("u", "c", MaxNow+1), KindInvalidArgument)
	assertKind(t, b.Add("", "c", 10), KindInvalidArgument)
	assertKind(t, b.Add("u", "", 10), KindInvalidArgument)
}

func snapshotsEqual(x, y Snapshot) string {
	if strings.Join(x.Order, ",") != strings.Join(y.Order, ",") {
		return "order"
	}
	if x.Rev != y.Rev {
		return "rev"
	}
	if x.LastTs != y.LastTs {
		return "lastTs"
	}
	return ""
}

func TestReorderSingleAndGroup(t *testing.T) {
	b := New()
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		mustAdd(t, b, "u", id, int64(10+i))
	}
	if err := b.Reorder("u", "c", "a", Below, b.Rev(), 20); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"c", "a", "b", "d", "e"})
	if err := b.Reorder("u", "e", "a", Above, b.Rev(), 21); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"c", "a", "e", "b", "d"})
	if err := b.Group("u", "g", []string{"b", "d"}, 22); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"c", "a", "e", "b", "d"})
	assertKind(t, b.Reorder("u", "b", "c", Above, b.Rev(), 23), KindIllegal)
	if err := b.Reorder("u", "g", "a", Below, b.Rev(), 24); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"c", "b", "d", "a", "e"})
	if b.elems["b"].lastRev != b.elems["d"].lastRev {
		t.Fatal("group members must share lastRev after move")
	}
	if err := b.Ungroup("u", "g", 25); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"c", "b", "d", "a", "e"})
}

func TestAnchorInsideOtherGroup(t *testing.T) {
	b := New()
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		mustAdd(t, b, "u", id, int64(10+i))
	}
	if err := b.Group("u", "g1", []string{"b", "c"}, 20); err != nil {
		t.Fatal(err)
	}
	if err := b.Reorder("u", "e", "c", Above, b.Rev(), 21); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"a", "b", "c", "e", "d"})
	if err := b.Reorder("u", "e", "c", Below, b.Rev(), 22); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"a", "b", "e", "c", "d"})
	if err := b.Reorder("u", "g1", "a", Below, b.Rev(), 23); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"b", "c", "a", "e", "d"})
}

func TestLockExpiryExactAndOneSecondBefore(t *testing.T) {
	b := New()
	mustAdd(t, b, "u1", "a", 0)
	mustAdd(t, b, "u2", "b", 0)
	if err := b.Lock("u1", "a", 10, 0); err != nil {
		t.Fatal(err)
	}
	assertKind(t, b.Lock("u2", "a", 1, 9), KindLocked)
	if err := b.Lock("u2", "a", 5, 10); err != nil {
		t.Fatalf("lock at exact expiry must succeed: %v", err)
	}
	var le *LockError
	err := b.Lock("u1", "a", 1, 11)
	if !errors.As(err, &le) || le.Holder != "u2" || le.Remain != 4 {
		t.Fatalf("u2 must hold lock with remain 4: %v", err)
	}
	assertKind(t, b.Unlock("u1", "a", 11), KindLocked)
	if err := b.Unlock("u2", "a", 11); err != nil {
		t.Fatal(err)
	}
	// 过期后 Unlock 不存在锁：任何用户对该 id 解锁都无副作用成功
	if err := b.Unlock("u1", "a", 100); err != nil {
		t.Fatal(err)
	}
}

func TestLockRenewal(t *testing.T) {
	b := New()
	mustAdd(t, b, "u1", "a", 0)
	if err := b.Lock("u1", "a", 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Lock("u1", "a", 2, 5); err != nil {
		t.Fatal(err)
	}
	// 续期后 expire=7：now=6 仍被 u1 持有，now=7 已到期
	assertKind(t, b.Lock("u2", "a", 1, 6), KindLocked)
	if err := b.Lock("u2", "a", 1, 7); err != nil {
		t.Fatalf("renewed lock must expire at 7: %v", err)
	}
}

func TestMoveNextToLockedAnchorAllowed(t *testing.T) {
	b := New()
	mustAdd(t, b, "u1", "a", 0)
	mustAdd(t, b, "u2", "b", 0)
	if err := b.Lock("u2", "b", 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Reorder("u1", "a", "b", Below, b.Rev(), 2); err != nil {
		t.Fatalf("moving next to locked anchor must pass: %v", err)
	}
	assertOrder(t, b, []string{"a", "b"})
}

func TestGroupLocksCoverMembers(t *testing.T) {
	b := New()
	mustAdd(t, b, "u1", "a", 0)
	mustAdd(t, b, "u1", "b", 0)
	mustAdd(t, b, "u2", "c", 0)
	if err := b.Group("u1", "g", []string{"a", "b"}, 1); err != nil {
		t.Fatal(err)
	}
	// 组合锁覆盖成员：他人 Ungroup 被拒
	if err := b.Lock("u1", "g", 100, 2); err != nil {
		t.Fatal(err)
	}
	assertKind(t, b.Ungroup("u2", "g", 3), KindLocked)
	// 成员锁使组合整体不可被他人操作
	if err := b.Unlock("u1", "g", 4); err != nil {
		t.Fatal(err)
	}
	if err := b.Lock("u1", "a", 100, 4); err != nil {
		t.Fatal(err)
	}
	assertKind(t, b.Reorder("u2", "g", "c", Above, b.Rev(), 5), KindLocked)
	if err := b.Reorder("u1", "g", "c", Above, b.Rev(), 6); err != nil {
		t.Fatalf("owner bypass own lock: %v", err)
	}
}

func TestVersionConflictBoundary(t *testing.T) {
	b := New()
	mustAdd(t, b, "u", "a", 0)
	mustAdd(t, b, "u", "b", 0)
	base := b.Rev()
	if err := b.Reorder("u", "a", "b", Above, base, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Reorder("u", "a", "b", Below, b.Rev(), 2); err != nil {
		t.Fatalf("equal baseRev must pass: %v", err)
	}
	assertKind(t, b.Reorder("u", "a", "b", Above, 1, 3), KindConflict)
}

func TestRemoveSemantics(t *testing.T) {
	b := New()
	for _, id := range []string{"a", "b", "c"} {
		mustAdd(t, b, "u", id, 0)
	}
	if err := b.Group("u", "g", []string{"a", "c"}, 1); err != nil {
		t.Fatal(err)
	}
	assertKind(t, b.Remove("u", "a", 2), KindIllegal)
	if err := b.Remove("u", "g", 3); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, b, []string{"b"})
	if _, ok := b.groups["g"]; ok {
		t.Fatal("group not removed")
	}
	assertKind(t, b.Remove("u", "g", 4), KindNotFound)
}

func TestRejectPrecedenceAllAdjacentPairs(t *testing.T) {
	// 非法参数 > 时钟回退
	b := New()
	mustAdd(t, b, "u", "a", 10)
	assertKind(t, b.Add("", "z", 1), KindInvalidArgument)

	// 时钟回退 > 不存在
	assertKind(t, b.Reorder("u", "ghost", "a", Above, 0, 9), KindClockBackward)

	// 不存在 > 不合法（target/anchor 分别缺失）
	assertKind(t, b.Reorder("u", "ghost", "a", Above, 0, 10), KindNotFound)
	assertKind(t, b.Reorder("u", "a", "ghost", Above, 0, 10), KindNotFound)

	// 不合法 > 锁定：a 属于组合 g，同时被他人锁定；直接移动成员先报不合法
	mustAdd(t, b, "u", "c", 10)
	mustAdd(t, b, "u", "d", 10)
	if err := b.Group("u", "g", []string{"a", "c"}, 11); err != nil {
		t.Fatal(err)
	}
	if err := b.Lock("other", "a", 100, 12); err != nil {
		t.Fatal(err)
	}
	assertKind(t, b.Reorder("other2", "a", "c", Above, 0, 13), KindIllegal)

	// 锁定 > 冲突：让 g 的成员 lastRev 大于 baseRev，再由他人持锁整体移动
	if err := b.Lock("other", "g", 100, 14); err != nil {
		t.Fatal(err)
	}
	err := b.Reorder("other2", "g", "d", Above, 0, 15)
	assertKind(t, err, KindLocked)

	// 冲突：持有者本人移动（锁不阻碍），baseRev 过旧
	assertKind(t, b.Reorder("other", "g", "d", Above, 0, 16), KindConflict)
}

func TestRejectZeroSideEffectDetailed(t *testing.T) {
	b := New()
	mustAdd(t, b, "u1", "a", 0)
	mustAdd(t, b, "u2", "b", 0)
	if err := b.Lock("u1", "a", 100, 1); err != nil {
		t.Fatal(err)
	}
	before := b.Snapshot(10)
	rejected := []error{
		b.Add("u2", "a", 10),                      // 重复 id（不合法）
		b.Reorder("u2", "b", "a", Side(9), 0, 10), // 参数非法
		b.Reorder("u2", "a", "b", Above, 0, 10),   // 他人锁
		b.Reorder("u2", "a", "b", Above, 99, 10),  // 先撞锁，仍无副作用
		b.Group("u2", "g2", []string{"a", "b"}, 10),
		b.Remove("u2", "a", 10),
		b.Lock("u2", "a", 10, 10),
		b.Unlock("u2", "a", 10),
		b.Add("u2", "c", 0), // 时钟回退
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("case %d expected reject", i)
		}
		after := b.Snapshot(10)
		if d := snapshotsEqual(before, after); d != "" {
			t.Fatalf("case %d changed state: %s", i, d)
		}
	}
	_ = rejected
}

func TestNamespaceShared(t *testing.T) {
	b := New()
	mustAdd(t, b, "u", "x", 0)
	assertKind(t, b.Group("u", "x", []string{"a"}, 1), KindInvalidArgument)
	mustAdd(t, b, "u", "y", 1)
	mustAdd(t, b, "u", "z", 1)
	assertKind(t, b.Group("u", "x", []string{"y", "z"}, 2), KindIllegal)
	if err := b.Group("u", "g", []string{"x", "y"}, 3); err != nil {
		t.Fatal(err)
	}
	assertKind(t, b.Add("u", "g", 4), KindIllegal)
}

func TestGroupMemberCountBounds(t *testing.T) {
	b := New()
	assertKind(t, b.Group("u", "g", []string{"a"}, 0), KindInvalidArgument)
	var ids []string
	for i := 0; i <= MaxGroupMembers; i++ {
		id := "e" + itoa(i)
		mustAdd(t, b, "u", id, int64(i))
		ids = append(ids, id)
	}
	assertKind(t, b.Group("u", "g", ids[:MinGroupMembers-1], int64(len(ids)+1)), KindInvalidArgument)
	assertKind(t, b.Group("u", "g", ids, int64(len(ids)+1)), KindInvalidArgument)
	if err := b.Group("u", "g", ids[:MinGroupMembers], int64(len(ids)+1)); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
