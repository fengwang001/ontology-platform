package reflog

import (
	"fmt"
	"sync"
	"testing"
)

func mustSystem(t *testing.T, cfg Config) *System {
	t.Helper()
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wcommit(t *testing.T, s *System, id CommitID, parents []CommitID, contents []ObjectID, size int64, now int64) {
	t.Helper()
	must(t, s.WriteCommit(id, parents, now, contents, size, now))
}

func wobject(t *testing.T, s *System, id ObjectID, size int64, now int64) {
	t.Helper()
	must(t, s.WriteObject(id, size, now))
}

// TestExpireBoundaryTiers covers the boundary (age == retention,
// which counts as expired) for both the reachable and the
// unreachable tier.
func TestExpireBoundaryTiers(t *testing.T) {
	cfg := Config{ReachableRetention: 10, UnreachableRetention: 5, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 1, 0)
	wcommit(t, s, "c2", []CommitID{"c1"}, nil, 1, 0)
	must(t, s.CreateRef("r", "c1", 1, "alice")) // rec1: old empty -> unreachable tier
	must(t, s.UpdateRef("r", "c2", 2, "alice")) // rec2: old c1 ancestor of c2 -> reachable tier

	// now=5: rec1 age 4 < 5 survives; rec2 age 3 < 10 survives.
	n, err := s.ExpireLogs(5)
	must(t, err)
	t.Logf("输入: rec1(t=1,不可达档U=5) rec2(t=2,可达档R=10), ExpireLogs(now=5)")
	t.Logf("实际输出: 过期条数=%d 剩余=%d", n, s.LogLen("r"))
	t.Logf("判定依据: 年龄4<5 与 年龄3<10 均未过期, 期望过期0条剩余2条")
	if n != 0 || s.LogLen("r") != 2 {
		t.Fatalf("got expired=%d len=%d, want 0/2", n, s.LogLen("r"))
	}

	// now=6: rec1 age 5 == 5 expires (boundary, unreachable tier).
	n, err = s.ExpireLogs(6)
	must(t, err)
	t.Logf("输入: ExpireLogs(now=6)")
	t.Logf("实际输出: 过期条数=%d 剩余=%d", n, s.LogLen("r"))
	t.Logf("判定依据: rec1 年龄5>=U5 取等过期, rec2 年龄4<R10 存活, 期望过期1条剩余1条")
	if n != 1 || s.LogLen("r") != 1 {
		t.Fatalf("got expired=%d len=%d, want 1/1", n, s.LogLen("r"))
	}

	// now=12: rec2 age 10 == 10 expires (boundary, reachable tier).
	n, err = s.ExpireLogs(12)
	must(t, err)
	t.Logf("输入: ExpireLogs(now=12)")
	t.Logf("实际输出: 过期条数=%d 剩余=%d", n, s.LogLen("r"))
	t.Logf("判定依据: rec2 年龄10>=R10 取等过期, 期望过期1条剩余0条")
	if n != 1 || s.LogLen("r") != 0 {
		t.Fatalf("got expired=%d len=%d, want 1/0", n, s.LogLen("r"))
	}
}

// TestAncestorClassificationMerge checks that the reachable tier is
// computed by real ancestry over a merge (diamond) history, not by
// first-parent or by recency.
func TestAncestorClassificationMerge(t *testing.T) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 10, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	// c0 <- c1, c0 <- c2, merge c3 = (c1, c2), then c4 on top of c1 only.
	wcommit(t, s, "c0", nil, nil, 1, 0)
	wcommit(t, s, "c1", []CommitID{"c0"}, nil, 1, 0)
	wcommit(t, s, "c2", []CommitID{"c0"}, nil, 1, 0)
	wcommit(t, s, "c3", []CommitID{"c1", "c2"}, nil, 1, 0)
	wcommit(t, s, "c4", []CommitID{"c1"}, nil, 1, 0)

	must(t, s.CreateRef("r", "c1", 1, "bob")) // rec1: old empty -> unreachable
	must(t, s.UpdateRef("r", "c2", 2, "bob")) // rec2: old c1, head c3: ancestor via merge parent 1 -> reachable
	must(t, s.UpdateRef("r", "c4", 3, "bob")) // rec3: old c2, head c3: ancestor via merge parent 2 -> reachable
	must(t, s.UpdateRef("r", "c3", 4, "bob")) // rec4: old c4, head c3: c4 not ancestor of c3 -> unreachable

	l := s.logs["r"]
	t.Logf("输入: 菱形合并历史 c3=(c1,c2), c4=(c1); 引用移动 c1->c2->c4->c3, 最终头为c3")
	t.Logf("实际输出: 可达档记录数=%d 不可达档记录数=%d", len(l.reachable), len(l.unreachable))
	t.Logf("判定依据: rec2/rec3 旧值 c1/c2 经合并的两条父边均为 c3 祖先(可达档); rec1 旧值为空与 rec4 旧值 c4 不是祖先(不可达档)")
	if len(l.reachable) != 2 || len(l.unreachable) != 2 {
		t.Fatalf("reachable=%d unreachable=%d, want 2/2", len(l.reachable), len(l.unreachable))
	}
	if l.reachable[0].Old != "c1" || l.reachable[1].Old != "c2" {
		t.Fatalf("reachable = %v %v, want old c1 and c2", l.reachable[0], l.reachable[1])
	}

	// Behavioral check via expiry: at now=14 the two unreachable
	// records (ages 13, 10 >= U=10) expire while the reachable ones
	// (ages 12, 11 < R=100) survive.
	n, err := s.ExpireLogs(14)
	must(t, err)
	t.Logf("输入: ExpireLogs(now=14)")
	t.Logf("实际输出: 过期条数=%d 剩余=%d", n, s.LogLen("r"))
	t.Logf("判定依据: rec1/rec4 不可达档年龄>=10过期, rec2/rec3 可达档年龄<100存活")
	if n != 2 || s.LogLen("r") != 2 {
		t.Fatalf("expired=%d len=%d, want 2/2", n, s.LogLen("r"))
	}
	rec, err := s.ReadLog("r", 1)
	must(t, err)
	if rec.Old != "c2" || rec.New != "c4" {
		t.Fatalf("survivor = %+v, want old=c2 new=c4", rec)
	}
}

// TestExpiredRecordLosesKeepAlive: an unexpired record keeps its old
// and new commits alive; once it expires, that protection is gone.
func TestExpiredRecordLosesKeepAlive(t *testing.T) {
	cfg := Config{ReachableRetention: 10, UnreachableRetention: 5, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	wobject(t, s, "o1", 7, 0)
	wcommit(t, s, "c1", nil, []ObjectID{"o1"}, 3, 0)
	wcommit(t, s, "c2", nil, nil, 4, 0) // independent of c1
	must(t, s.CreateRef("r", "c1", 0, "carol"))
	must(t, s.UpdateRef("r", "c2", 1, "carol")) // rec: old c1, c1 not ancestor of c2 -> unreachable tier

	// now=5: record age 4 < 5, still keeps c1 (and o1 via c1) alive.
	st, err := s.GC(5)
	must(t, err)
	t.Logf("输入: rec(t=1,old=c1,new=c2,U=5), GC(now=5)")
	t.Logf("实际输出: %+v c1存在=%v o1存在=%v", st, s.HasCommit("c1"), s.HasObject("o1"))
	t.Logf("判定依据: 记录未过期, c1为存活根, o1经c1内容引用可达, 期望删除0")
	if st.Commits != 0 || st.Objects != 0 || !s.HasCommit("c1") || !s.HasObject("o1") {
		t.Fatalf("st=%+v c1=%v o1=%v, want nothing deleted", st, s.HasCommit("c1"), s.HasObject("o1"))
	}

	// Expire at now=6 (record age 5 >= 5), then GC: c1 and o1 go.
	n, err := s.ExpireLogs(6)
	must(t, err)
	st, err = s.GC(6)
	must(t, err)
	t.Logf("输入: ExpireLogs(now=6) 后 GC(now=6)")
	t.Logf("实际输出: 过期条数=%d %+v c1存在=%v o1存在=%v", n, st, s.HasCommit("c1"), s.HasObject("o1"))
	t.Logf("判定依据: 记录过期后不再保活, c1与o1不可达且宽限0, 期望删1提交1对象共10字节; c2是引用当前值不受影响")
	if n != 2 || st.Commits != 1 || st.Objects != 1 || st.Bytes != 10 {
		t.Fatalf("expired=%d st=%+v, want 2 and {1 1 10}", n, st)
	}
	if s.HasCommit("c1") || s.HasObject("o1") || !s.HasCommit("c2") {
		t.Fatalf("c1=%v o1=%v c2=%v", s.HasCommit("c1"), s.HasObject("o1"), s.HasCommit("c2"))
	}
}

// TestFreshnessGraceBoundary: age == grace counts as grace satisfied
// (deletable); age == grace-1 does not.
func TestFreshnessGraceBoundary(t *testing.T) {
	cfg := Config{ReachableRetention: 10, UnreachableRetention: 5, FreshnessGrace: 5}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 2, 0) // orphan, first written at 0
	wobject(t, s, "o1", 3, 0)           // orphan object

	st, err := s.GC(4)
	must(t, err)
	t.Logf("输入: c1/o1 首次写入t=0, 宽限=5, GC(now=4)")
	t.Logf("实际输出: %+v", st)
	t.Logf("判定依据: 年龄4<5 不足宽限, 期望删除0")
	if st.Commits != 0 || st.Objects != 0 {
		t.Fatalf("st=%+v, want zero", st)
	}

	st, err = s.GC(5)
	must(t, err)
	t.Logf("输入: GC(now=5)")
	t.Logf("实际输出: %+v", st)
	t.Logf("判定依据: 年龄5>=5 取等可删, 期望删1提交1对象共5字节")
	if st.Commits != 1 || st.Objects != 1 || st.Bytes != 5 {
		t.Fatalf("st=%+v, want {1 1 5}", st)
	}
}

// TestGCIdempotent: a second GC with no intervening change reclaims
// nothing.
func TestGCIdempotent(t *testing.T) {
	cfg := Config{ReachableRetention: 10, UnreachableRetention: 5, FreshnessGrace: 2}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 2, 0)
	wobject(t, s, "o1", 3, 0)

	st1, err := s.GC(10)
	must(t, err)
	st2, err := s.GC(10)
	must(t, err)
	t.Logf("输入: 同一now=10连续两次GC, 中间无变化")
	t.Logf("实际输出: 第一次=%+v 第二次=%+v", st1, st2)
	t.Logf("判定依据: 第一次回收全部超宽限垃圾, 第二次必须删除0个对象")
	if st1.Commits != 1 || st1.Objects != 1 || st1.Bytes != 5 {
		t.Fatalf("first GC = %+v, want {1 1 5}", st1)
	}
	if st2.Commits != 0 || st2.Objects != 0 || st2.Bytes != 0 {
		t.Fatalf("second GC = %+v, want zero", st2)
	}
}

// buildTwinScenario builds the scenario used to prove that the
// combined ExpireAndGC equals ExpireLogs+GC.
func buildTwinScenario(t *testing.T) *System {
	t.Helper()
	cfg := Config{ReachableRetention: 10, UnreachableRetention: 4, FreshnessGrace: 2}
	s := mustSystem(t, cfg)
	wobject(t, s, "o1", 5, 0)
	wcommit(t, s, "c1", nil, []ObjectID{"o1"}, 3, 0)
	wcommit(t, s, "c2", []CommitID{"c1"}, nil, 4, 0)
	wcommit(t, s, "c3", nil, nil, 6, 0) // orphan, will be past grace
	must(t, s.CreateRef("r", "c1", 1, "dave"))
	must(t, s.UpdateRef("r", "c2", 2, "dave"))
	must(t, s.DeleteRef("r", 3, "dave"))
	return s
}

func snapshot(t *testing.T, s *System) string {
	t.Helper()
	out := fmt.Sprintf("commits:")
	for _, id := range []CommitID{"c1", "c2", "c3"} {
		out += fmt.Sprintf(" %s=%v", id, s.HasCommit(id))
	}
	out += fmt.Sprintf(" objects: o1=%v logLen(r)=%d", s.HasObject("o1"), s.LogLen("r"))
	return out
}

// TestCombinedEqualsSequential: ExpireAndGC(now) must produce exactly
// the same result as ExpireLogs(now) followed by GC(now).
func TestCombinedEqualsSequential(t *testing.T) {
	a := buildTwinScenario(t)
	b := buildTwinScenario(t)

	expA, stA, err := a.ExpireAndGC(20)
	must(t, err)
	expB, err := b.ExpireLogs(20)
	must(t, err)
	stB, err := b.GC(20)
	must(t, err)

	t.Logf("输入: 相同场景两份, now=20; A执行合并, B先过期后回收")
	t.Logf("实际输出: A(合并) 过期=%d %+v %s", expA, stA, snapshot(t, a))
	t.Logf("实际输出: B(分开) 过期=%d %+v %s", expB, stB, snapshot(t, b))
	t.Logf("判定依据: 合并=先过期后回收, 过期条数/回收统计/最终状态必须完全一致")
	if expA != expB || stA != stB || snapshot(t, a) != snapshot(t, b) {
		t.Fatalf("combined %+v/%d/%s != sequential %+v/%d/%s",
			stA, expA, snapshot(t, a), stB, expB, snapshot(t, b))
	}
}

// TestDeletedRefAllUnreachable: after deletion, every record of the
// reference falls under the unreachable tier.
func TestDeletedRefAllUnreachable(t *testing.T) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 10, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 1, 0)
	wcommit(t, s, "c2", []CommitID{"c1"}, nil, 1, 0)
	must(t, s.CreateRef("r", "c1", 0, "erin"))
	must(t, s.UpdateRef("r", "c2", 1, "erin")) // would be reachable tier while r lives
	must(t, s.DeleteRef("r", 2, "erin"))

	n, err := s.ExpireLogs(12)
	must(t, err)
	t.Logf("输入: r在t=2被删除, 三条记录(t=0,1,2), R=100 U=10, ExpireLogs(now=12)")
	t.Logf("实际输出: 过期条数=%d 剩余=%d", n, s.LogLen("r"))
	t.Logf("判定依据: 已删除引用的全部记录按不可达档, 年龄12/11/10均>=10, 期望3条全过期")
	if n != 3 || s.LogLen("r") != 0 {
		t.Fatalf("expired=%d len=%d, want 3/0", n, s.LogLen("r"))
	}
}

// TestRecreateSharesLog: recreating a deleted name continues the same
// log; old records remain readable.
func TestRecreateSharesLog(t *testing.T) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 100, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 1, 0)
	wcommit(t, s, "c2", nil, nil, 1, 0)
	must(t, s.CreateRef("r", "c1", 1, "frank"))
	must(t, s.DeleteRef("r", 2, "frank"))
	must(t, s.CreateRef("r", "c2", 3, "frank")) // recreate

	r1, e1 := s.ReadLog("r", 1)
	r2, e2 := s.ReadLog("r", 2)
	r3, e3 := s.ReadLog("r", 3)
	_, e4 := s.ReadLog("r", 4)
	t.Logf("输入: r 创建(t=1)->删除(t=2)->同名重建(t=3)")
	t.Logf("实际输出: 序号1=%+v 序号2=%+v 序号3=%+v 序号4错误=%v", r1, r2, r3, e4)
	t.Logf("判定依据: 三条记录同属一条日志, 序号从最新往旧: 1=重建(旧空), 2=删除(新空), 3=创建(旧空)")
	must(t, e1)
	must(t, e2)
	must(t, e3)
	if r1.Old != "" || r1.New != "c2" || r1.Time != 3 {
		t.Fatalf("rec1 = %+v", r1)
	}
	if r2.Old != "c1" || r2.New != "" || r2.Time != 2 {
		t.Fatalf("rec2 = %+v", r2)
	}
	if r3.Old != "" || r3.New != "c1" || r3.Time != 1 {
		t.Fatalf("rec3 = %+v", r3)
	}
	if e4 != ErrRecordNotFound {
		t.Fatalf("index 4 err = %v, want ErrRecordNotFound", e4)
	}
}

// TestNeverExistedVsAllExpired distinguishes a reference that never
// existed from one whose records have all expired.
func TestNeverExistedVsAllExpired(t *testing.T) {
	cfg := Config{ReachableRetention: 5, UnreachableRetention: 5, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 1, 0)
	must(t, s.CreateRef("r", "c1", 0, "gina"))
	n, err := s.ExpireLogs(100)
	must(t, err)

	_, errNever := s.ReadLog("ghost", 1)
	_, errGone := s.ReadLog("r", 1)
	t.Logf("输入: ghost从未存在; r曾存在且%d条记录已全部过期", n)
	t.Logf("实际输出: ghost->%v r->%v", errNever, errGone)
	t.Logf("判定依据: 从未存在报「引用不存在」, 曾存在但记录全过期报「记录不存在」")
	if errNever != ErrRefNotFound {
		t.Fatalf("ghost err = %v, want ErrRefNotFound", errNever)
	}
	if errGone != ErrRecordNotFound {
		t.Fatalf("r err = %v, want ErrRecordNotFound", errGone)
	}
}

// TestClockRegressionNoEffect: a regressed call is rejected and has
// no effect: no record appended, no object deleted, clock not
// advanced.
func TestClockRegressionNoEffect(t *testing.T) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 100, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 1, 0)
	wcommit(t, s, "c2", nil, nil, 1, 0)
	must(t, s.CreateRef("r", "c1", 10, "henry"))

	err := s.UpdateRef("r", "c2", 5, "henry") // regressed: 5 < 10
	head, _ := s.Head("r")
	t.Logf("输入: 已接受t=10后, UpdateRef(r,c2,now=5)")
	t.Logf("实际输出: 错误=%v 当前值=%s 日志条数=%d", err, head, s.LogLen("r"))
	t.Logf("判定依据: 时钟回退报错且不生效: 不追加日志, 当前值不变")
	if err != ErrClockRegression {
		t.Fatalf("err = %v, want ErrClockRegression", err)
	}
	if head != "c1" || s.LogLen("r") != 1 {
		t.Fatalf("head=%s len=%d, want c1/1", head, s.LogLen("r"))
	}

	// A rejected call must not advance the clock: now=12 is rejected
	// (invalid: empty name), so now=11 must still be accepted.
	if err := s.DeleteRef("", 12, "henry"); err != ErrInvalidParam {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
	must(t, s.UpdateRef("r", "c2", 11, "henry"))
	t.Logf("输入: 拒绝now=12的非法调用后, UpdateRef(r,c2,now=11)")
	t.Logf("实际输出: 接受, 当前值=c2")
	t.Logf("判定依据: 被拒绝的调用不推进时钟, 11>=10仍可接受")

	// A regressed GC deletes nothing.
	wcommit(t, s, "c3", nil, nil, 1, 11) // orphan, but GC below is rejected
	_, err = s.GC(3)
	t.Logf("输入: GC(now=3) 回退")
	t.Logf("实际输出: 错误=%v c3存在=%v", err, s.HasCommit("c3"))
	t.Logf("判定依据: 回退的回收不生效, 不删除任何对象")
	if err != ErrClockRegression || !s.HasCommit("c3") {
		t.Fatalf("err=%v c3=%v", err, s.HasCommit("c3"))
	}
}

// TestErrorOrdering covers every adjacent pair of the mandated error
// precedence: invalid < clock < ref < commit < record.
func TestErrorOrdering(t *testing.T) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 100, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	wcommit(t, s, "c1", nil, nil, 1, 0)
	must(t, s.CreateRef("r", "c1", 10, "iris"))

	// Pair invalid < clock: empty name AND regressed time.
	err := s.UpdateRef("", "c1", 5, "iris")
	t.Logf("输入: UpdateRef(空名, now=5回退)")
	t.Logf("实际输出: %v", err)
	t.Logf("判定依据: 参数非法优先于时钟回退")
	if err != ErrInvalidParam {
		t.Fatalf("got %v, want ErrInvalidParam", err)
	}

	// Pair clock < ref: regressed time AND unknown reference.
	err = s.UpdateRef("ghost", "c1", 5, "iris")
	t.Logf("输入: UpdateRef(ghost不存在, now=5回退)")
	t.Logf("实际输出: %v", err)
	t.Logf("判定依据: 时钟回退优先于引用不存在")
	if err != ErrClockRegression {
		t.Fatalf("got %v, want ErrClockRegression", err)
	}

	// Pair ref < commit: unknown reference AND unknown commit.
	err = s.UpdateRef("ghost", "nope", 20, "iris")
	t.Logf("输入: UpdateRef(ghost不存在, 提交nope不存在, now=20)")
	t.Logf("实际输出: %v", err)
	t.Logf("判定依据: 引用不存在优先于提交不存在")
	if err != ErrRefNotFound {
		t.Fatalf("got %v, want ErrRefNotFound", err)
	}

	// Pair commit < record: no single API call can produce both a
	// commit error and a record error (only ReadLog indexes records
	// and it takes no commit). The pair is therefore verified against
	// the declared precedence table, and each error is exercised
	// individually below.
	rank := make(map[error]int)
	for i, e := range errPrecedence {
		rank[e] = i
	}
	t.Logf("输入: 声明次序表 %v", errPrecedence)
	t.Logf("实际输出: rank(提交不存在)=%d rank(记录不存在)=%d",
		rank[ErrCommitNotFound], rank[ErrRecordNotFound])
	t.Logf("判定依据: 无单一调用同时产生这两类错误, 依据声明次序表验证提交不存在排在记录不存在之前")
	if rank[ErrCommitNotFound] >= rank[ErrRecordNotFound] {
		t.Fatalf("declared order wrong: %v", errPrecedence)
	}
	if err := s.CreateRef("x", "nope", 21, "iris"); err != ErrCommitNotFound {
		t.Fatalf("got %v, want ErrCommitNotFound", err)
	}
	if _, err := s.ReadLog("r", 99); err != ErrRecordNotFound {
		t.Fatalf("got %v, want ErrRecordNotFound", err)
	}
}

// TestConcurrentSameTimeUpdates: concurrent updates of one reference
// at the same time all succeed, and their log order follows the
// order in which they acquired the write lock.
func TestConcurrentSameTimeUpdates(t *testing.T) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 100, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	const n = 32
	for i := 0; i < n; i++ {
		wcommit(t, s, CommitID(fmt.Sprintf("c%d", i)), nil, nil, 1, 0)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.CreateRef("r", CommitID(fmt.Sprintf("c%d", i)), 50, "race")
		}(i)
	}
	wg.Wait()

	l := s.logs["r"]
	all := l.merged()
	t.Logf("输入: %d个goroutine同时CreateRef(r, cN, now=50)", n)
	t.Logf("实际输出: 日志条数=%d 首条时间=%d 末条时间=%d", len(all), all[0].Time, all[len(all)-1].Time)
	t.Logf("判定依据: 时刻相同以写入成功先后为序, 全部成功且序号0..%d递增", n-1)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if len(all) != n {
		t.Fatalf("len = %d, want %d", len(all), n)
	}
	for i, rec := range all {
		if rec.Time != 50 || rec.Seq != uint64(i) {
			t.Fatalf("record %d = %+v", i, rec)
		}
	}
}

// TestConcurrentDistinctTimeUpdates: for concurrent updates with
// distinct times, the log order always matches the time order; a
// late loser with an earlier time is rejected as a clock regression.
func TestConcurrentDistinctTimeUpdates(t *testing.T) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 100, FreshnessGrace: 0}
	s := mustSystem(t, cfg)
	const n = 64
	for i := 0; i < n; i++ {
		wcommit(t, s, CommitID(fmt.Sprintf("c%d", i)), nil, nil, 1, 0)
	}
	must(t, s.CreateRef("r", "c0", 100, "setup"))

	var wg sync.WaitGroup
	for i := 1; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct times, raced: losers with earlier times than
			// an already-accepted call are rejected.
			_ = s.UpdateRef("r", CommitID(fmt.Sprintf("c%d", i)), 100+int64(i), "race")
		}(i)
	}
	wg.Wait()

	l := s.logs["r"]
	all := l.merged()
	t.Logf("输入: %d个goroutine并发UpdateRef, 时刻100+i各不相同", n-1)
	t.Logf("实际输出: 日志条数=%d", len(all))
	t.Logf("判定依据: 日志时刻只增不减, 且序号与时刻同序")
	for i := 1; i < len(all); i++ {
		if all[i].Time < all[i-1].Time {
			t.Fatalf("time regression in log: %v -> %v", all[i-1], all[i])
		}
		if all[i].Seq <= all[i-1].Seq {
			t.Fatalf("seq not increasing: %v -> %v", all[i-1], all[i])
		}
	}
}

// TestConcurrentGCAndUpdate: a GC racing a reference update must be
// equivalent to one of the two serial orders; in particular it must
// never delete the commit the update just pointed at.
func TestConcurrentGCAndUpdate(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		cfg := Config{ReachableRetention: 100, UnreachableRetention: 100, FreshnessGrace: 0}
		s := mustSystem(t, cfg)
		wcommit(t, s, "c", nil, nil, 1, 0)    // orphan, past grace
		wcommit(t, s, "base", nil, nil, 1, 0) // keeps ref alive
		must(t, s.CreateRef("r", "base", 1, "setup"))

		var updateErr error
		var stats GCStats
		var gcErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); updateErr = s.UpdateRef("r", "c", 10, "race") }()
		go func() { defer wg.Done(); stats, gcErr = s.GC(10) }()
		wg.Wait()

		alive := s.HasCommit("c")
		if trial == 0 {
			t.Logf("输入: 孤儿提交c(宽限0)并发 UpdateRef(r,c,10) 与 GC(10)")
			t.Logf("实际输出: updateErr=%v gc=%+v gcErr=%v c存活=%v", updateErr, stats, gcErr, alive)
			t.Logf("判定依据: 串行等价——更新先则c存活且更新成功; 回收先则c被删且更新报提交不存在")
		}
		if gcErr != nil {
			t.Fatalf("trial %d: gc err %v", trial, gcErr)
		}
		switch {
		case updateErr == nil && !alive:
			t.Fatalf("trial %d: update succeeded but c was deleted", trial)
		case updateErr == ErrCommitNotFound && alive:
			t.Fatalf("trial %d: update rejected but c survived", trial)
		case updateErr != nil && updateErr != ErrCommitNotFound:
			t.Fatalf("trial %d: unexpected update err %v", trial, updateErr)
		}
	}
}

// TestConcurrentMixedRace hammers the system with concurrent updates,
// expiries, GCs and reads; run with -race. The observable result must
// equal some serial order, checked here through global invariants.
func TestConcurrentMixedRace(t *testing.T) {
	cfg := Config{ReachableRetention: 8, UnreachableRetention: 3, FreshnessGrace: 2}
	s := mustSystem(t, cfg)
	const commits = 16
	for i := 0; i < commits; i++ {
		wcommit(t, s, CommitID(fmt.Sprintf("c%d", i)), nil, nil, 1, 0)
	}
	must(t, s.CreateRef("r", "c0", 0, "setup"))

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				now := int64(1 + (g*50+i)%40)
				switch i % 4 {
				case 0:
					_ = s.UpdateRef("r", CommitID(fmt.Sprintf("c%d", (g+i)%commits)), now, "race")
				case 1:
					_, _ = s.ExpireLogs(now)
				case 2:
					_, _ = s.GC(now)
				case 3:
					_, _ = s.ReadLog("r", 1)
				}
			}
		}(g)
	}
	wg.Wait()

	l := s.logs["r"]
	all := l.merged()
	t.Logf("输入: 8个goroutine混合并发 更新/过期/回收/查询")
	t.Logf("实际输出: 日志条数=%d", len(all))
	t.Logf("判定依据: 日志时刻只增不减且序号递增(串行等价的必要条件)")
	for i := 1; i < len(all); i++ {
		if all[i].Time < all[i-1].Time || all[i].Seq <= all[i-1].Seq {
			t.Fatalf("log order broken: %v -> %v", all[i-1], all[i])
		}
	}
	// Whatever the interleaving, the current head must be alive.
	head, ok := s.Head("r")
	if ok && !s.HasCommit(head) {
		t.Fatalf("head %s was collected", head)
	}
}
