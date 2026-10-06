package pathlock

import (
	"errors"
	"reflect"
	"testing"
)

func asLockErr(t *testing.T, err error) *LockError {
	t.Helper()
	var le *LockError
	if !errors.As(err, &le) {
		t.Fatalf("期望 *LockError, 实际 %T: %v", err, err)
	}
	return le
}

func mustAcquire(t *testing.T, s *Service, user, path string) Lock {
	t.Helper()
	l, err := s.Acquire(user, path)
	if err != nil {
		t.Fatalf("Acquire(%q,%q) 意外失败: %v", user, path, err)
	}
	t.Logf("输入 Acquire(user=%q path=%q) 实际 id=%q owner=%q 判定=成功",
		user, path, l.ID, l.Owner)
	return l
}

func pathsOf(ls []Lock) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Path
	}
	return out
}

func idOf(t *testing.T, s *Service, path string) string {
	t.Helper()
	l, ok := treapGet(s.root, path)
	if !ok {
		t.Fatalf("路径 %q 无锁", path)
	}
	return l.ID
}

// 本人与他人精确冲突可区分，且重复加锁不改变 id 与创建时刻。
func TestAcquireSelfVsOther(t *testing.T) {
	s := NewService()
	l1 := mustAcquire(t, s, "alice", "a/b")

	_, err := s.Acquire("alice", "/a//b/")
	le := asLockErr(t, err)
	t.Logf("本人重复加锁: code=%d conflictID=%q 判定=已由本人持有", le.Code, le.Conflict.ID)
	if le.Code != ErrCodeSelfHeld {
		t.Fatalf("期望 self-held, 得 code=%d", le.Code)
	}
	if le.Conflict.ID != l1.ID {
		t.Fatalf("冲突锁应为原锁 %q, 得 %q", l1.ID, le.Conflict.ID)
	}

	_, err = s.Acquire("bob", "a/b")
	le = asLockErr(t, err)
	t.Logf("他人加锁: code=%d conflictOwner=%q 判定=已被他人持有", le.Code, le.Conflict.Owner)
	if le.Code != ErrCodeOtherHeld || le.Conflict.Owner != "alice" {
		t.Fatalf("期望 other-held(alice), 得 %+v", le)
	}

	got, _ := treapGet(s.root, "a/b")
	if got.ID != l1.ID || !got.CreatedAt.Equal(l1.CreatedAt) {
		t.Fatalf("原锁属性被改变: %+v vs %+v", got, l1)
	}
}

// 祖先/后代双向排他；本人可越过；冲突锁取最短路径、同长取字节序最小。
func TestAncestorDescendant(t *testing.T) {
	s := NewService()
	mustAcquire(t, s, "alice", "a/b/c/d")

	_, err := s.Acquire("bob", "a")
	le := asLockErr(t, err)
	t.Logf("bob 锁祖先 a: code=%d conflict=%q 判定=祖先冲突", le.Code, le.Conflict.Path)
	if le.Code != ErrCodeAncestorConflict || le.Conflict.Path != "a/b/c/d" {
		t.Fatalf("祖先冲突裁决错误: %+v", le)
	}

	_, err = s.Acquire("bob", "a/b/c/d/e/f")
	le = asLockErr(t, err)
	if le.Code != ErrCodeAncestorConflict || le.Conflict.Path != "a/b/c/d" {
		t.Fatalf("后代冲突裁决错误: %+v", le)
	}

	mustAcquire(t, s, "alice", "a")
	_, err = s.Acquire("bob", "a/b/x")
	le = asLockErr(t, err)
	t.Logf("多冲突挑选: conflict=%q 判定=最短路径", le.Conflict.Path)
	if le.Conflict.Path != "a" {
		t.Fatalf("应挑最短锁 a, 得 %q", le.Conflict.Path)
	}

	mustAcquire(t, s, "bob", "x/a00")
	mustAcquire(t, s, "bob", "x/a01")
	_, err = s.Acquire("carol", "x")
	le = asLockErr(t, err)
	t.Logf("等长冲突挑选: conflict=%q 判定=字节序最小", le.Conflict.Path)
	if le.Conflict.Path != "x/a00" {
		t.Fatalf("等长应取 x/a00, 得 %q", le.Conflict.Path)
	}

	mustAcquire(t, s, "alice", "a/b/c/d/child")
}

// 释放：非持有者、无权强制、锁不存在、不可二次释放、强制释放审计。
func TestReleaseRules(t *testing.T) {
	s := NewService()
	l := mustAcquire(t, s, "alice", "a/b")

	if _, err := s.Release("bob", l.ID, false); asLockErr(t, err).Code != ErrCodeNotOwner {
		t.Fatalf("期望非持有者")
	}
	if _, ok := treapGet(s.root, "a/b"); !ok {
		t.Fatalf("被拒绝的释放不得改动锁")
	}
	t.Logf("bob 普通释放 -> 非持有者；锁仍在")

	if _, err := s.Release("bob", l.ID, true); asLockErr(t, err).Code != ErrCodeNotAdmin {
		t.Fatalf("期望无权强制释放")
	}
	if n := len(s.AuditLog()); n != 0 {
		t.Fatalf("被拒绝的强制释放不得写审计, 得 %d 条", n)
	}

	if _, err := s.Release("admin", "lock-nope", true); asLockErr(t, err).Code != ErrCodeLockNotFound {
		t.Fatalf("期望锁不存在")
	}

	rl, err := s.Release("admin", l.ID, true)
	if err != nil {
		t.Fatalf("管理员强制释放失败: %v", err)
	}
	t.Logf("admin 强制释放 id=%q path=%q 原 owner=%q", rl.ID, rl.Path, rl.Owner)
	audits := s.AuditLog()
	if len(audits) != 1 || audits[0].LockID != l.ID || audits[0].Admin != "admin" ||
		audits[0].Owner != "alice" || audits[0].Seq <= 0 {
		t.Fatalf("审计内容错误: %+v", audits)
	}

	if _, err := s.Release("alice", l.ID, false); asLockErr(t, err).Code != ErrCodeLockNotFound {
		t.Fatalf("二次释放应报锁不存在")
	}

	l2 := mustAcquire(t, s, "alice", "c")
	if _, err := s.Release("alice", l2.ID, false); err != nil {
		t.Fatalf("本人释放失败: %v", err)
	}
	if n := len(s.AuditLog()); n != 1 {
		t.Fatalf("普通释放不得写审计, 得 %d 条", n)
	}
}

// 批校验：全有或全无、冲突完整且按字节序、校验零副作用。
func TestVerifyAllOrNothing(t *testing.T) {
	s := NewService()
	la := mustAcquire(t, s, "alice", "a/x")
	mustAcquire(t, s, "bob", "b/y")

	res, err := s.Verify("carol", []string{"a/x", "b/y", "clean"}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("carol 批校验: allowed=%v conflicts=%v", res.Allowed, res.Conflicts)
	if res.Allowed {
		t.Fatal("应整批拒绝")
	}
	want := []Conflict{{Path: "a/x", Owner: "alice"}, {Path: "b/y", Owner: "bob"}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("冲突列表错误: %+v", res.Conflicts)
	}

	mustAcquire(t, s, "bob", "d")
	res, _ = s.Verify("carol", []string{"d/e"}, false)
	if res.Allowed || !reflect.DeepEqual(res.Conflicts,
		[]Conflict{{Path: "d", Owner: "bob"}}) {
		t.Fatalf("祖先锁应冲突: %+v", res)
	}

	res, _ = s.Verify("alice", []string{"a/x", "clean"}, false)
	if !res.Allowed || len(res.Conflicts) != 0 {
		t.Fatalf("本人锁应放行: %+v", res)
	}
	if _, ok := treapGet(s.root, la.Path); !ok {
		t.Fatal("校验后本人锁丢失")
	}
}

// 校验并顺带释放：放行释放本人精确锁；拒绝时一把不释放。
func TestVerifyAndRelease(t *testing.T) {
	s := NewService()
	l1 := mustAcquire(t, s, "alice", "a")
	l2 := mustAcquire(t, s, "alice", "b")
	mustAcquire(t, s, "bob", "c")

	res, err := s.Verify("alice", []string{"a", "b", "d"}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("放行顺带释放: ids=%v", res.ReleasedIDs)
	if !res.Allowed || len(res.ReleasedIDs) != 2 {
		t.Fatalf("应释放 2 把本人锁: %+v", res)
	}
	freed := map[string]bool{l1.ID: false, l2.ID: false}
	for _, id := range res.ReleasedIDs {
		freed[id] = true
	}
	if !freed[l1.ID] || !freed[l2.ID] {
		t.Fatalf("释放集合错误: %v", freed)
	}
	if _, ok := treapGet(s.root, "c"); !ok {
		t.Fatal("他人锁不应被动")
	}

	mustAcquire(t, s, "alice", "e")
	before := len(s.pathOf)
	res, _ = s.Verify("alice", []string{"e", "c"}, true)
	t.Logf("拒绝顺带释放: allowed=%v released=%v 锁数 %d->%d",
		res.Allowed, res.ReleasedIDs, before, len(s.pathOf))
	if res.Allowed || len(res.ReleasedIDs) != 0 || len(s.pathOf) != before {
		t.Fatal("拒绝时必须一把不释放")
	}
	if _, ok := treapGet(s.root, "e"); !ok {
		t.Fatal("被拒绝批次中本人锁丢失")
	}
}

// 翻页期间插入/删除：快照保证整把出现、不重复、不遗漏。
func TestPaginationSnapshot(t *testing.T) {
	s := NewService()
	for _, p := range []string{"k1", "k2", "k3", "k4", "k5"} {
		mustAcquire(t, s, "alice", p)
	}

	page1, err := s.List("", "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := pathsOf(page1.Locks); !reflect.DeepEqual(got, []string{"k1", "k2"}) {
		t.Fatalf("第1页错误: %v", got)
	}
	t.Logf("第1页=%v snap=%q", pathsOf(page1.Locks), page1.Snapshot)

	mustAcquire(t, s, "bob", "k2a")
	k4 := idOf(t, s, "k4")
	if _, err := s.Release("alice", k4, false); err != nil {
		t.Fatal(err)
	}
	mustAcquire(t, s, "bob", "k6")

	page2, err := s.List("", page1.NextToken, page1.Snapshot, 2)
	if err != nil {
		t.Fatal(err)
	}
	page3, err := s.List("", page2.NextToken, page1.Snapshot, 2)
	if err != nil {
		t.Fatal(err)
	}
	all := append(pathsOf(page2.Locks), pathsOf(page3.Locks)...)
	t.Logf("快照续页=%v hasMore=%v 判定=建快照时的 k3,k4,k5", all, page3.HasMore)
	if !reflect.DeepEqual(all, []string{"k3", "k4", "k5"}) {
		t.Fatalf("快照必须看到建快照时的世界: %v", all)
	}
	if page3.HasMore {
		t.Fatal("最后一页不应有续页标记")
	}

	fresh, _ := s.List("", "", "", 100)
	want := []string{"k1", "k2", "k2a", "k3", "k5", "k6"}
	if got := pathsOf(fresh.Locks); !reflect.DeepEqual(got, want) {
		t.Fatalf("新快照世界错误: %v want %v", got, want)
	}
	s.ReleaseSnapshot(page1.Snapshot)
}

// 错误次序：每对相邻错误码前者优先。
func TestErrorPrecedence(t *testing.T) {
	s := NewService()
	locked := mustAcquire(t, s, "alice", "a/b")
	type probe struct {
		name string
		want int
		err  error
	}
	probes := []probe{
		{"非法路径 先于 空用户", ErrCodeInvalidPath, firstErr(s.Acquire("", "../x"))},
		{"空用户 先于 空批次", ErrCodeEmptyUser, verr(s.Verify("", nil, false))},
		{"空批次 先于 无权", ErrCodeEmptyBatch, verr(s.Verify("bob", []string{}, true))},
		{"无权 先于 锁不存在", ErrCodeNotAdmin, firstErr(s.Release("bob", "missing", true))},
		{"锁不存在 先于 非持有者", ErrCodeLockNotFound, firstErr(s.Release("bob", "missing", false))},
		{"非持有者 先于 本人持有", ErrCodeNotOwner, firstErr(s.Release("bob", locked.ID, false))},
		{"本人持有 先于 他人持有", ErrCodeSelfHeld, firstErr(s.Acquire("alice", "a/b"))},
		{"他人持有 先于 祖先后代冲突", ErrCodeOtherHeld, firstErr(s.Acquire("bob", "a/b"))},
	}
	for _, p := range probes {
		le := asLockErr(t, p.err)
		t.Logf("%s: 实际 code=%d 期望 code=%d 判定依据=错误次序", p.name, le.Code, p.want)
		if le.Code != p.want {
			t.Fatalf("%s: 得 %d 想 %d", p.name, le.Code, p.want)
		}
	}
}

func firstErr(_ Lock, err error) error     { return err }
func verr(_ VerifyResult, err error) error { return err }
