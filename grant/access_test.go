package grant

import (
	"testing"

	"ontology/ledger"
	"ontology/policy"
)

// Access 的 t 恰等时钟合法、大 1 为未来时刻错误；资源非法与负 t 为参数错误。
func TestAccessClockEdges(t *testing.T) {
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Access("alice", "/db/prod", 10); err != nil {
		t.Fatalf("t==clock: %v", err)
	}
	if _, err := svc.Access("alice", "/db/prod", 11); err != ErrFuture {
		t.Fatalf("t==clock+1 = %v, want ErrFuture", err)
	}
	if _, err := svc.Access("alice", "/db/prod", -1); err != ErrParam {
		t.Fatalf("t<0 = %v, want ErrParam", err)
	}
	if _, err := svc.Access("alice", "db/prod", 5); err != ErrParam {
		t.Fatalf("bad resource = %v, want ErrParam", err)
	}
	ok, err := svc.Access("ghost", "/db/prod", 10)
	if err != nil || ok {
		t.Fatalf("未登记 principal: ok=%v err=%v, want false,nil", ok, err)
	}
}

// 作用域段边界："/d" 不覆盖 "/db/prod"，"/db" 覆盖 "/db/prod/users"。
func TestScopeSegmentBoundary(t *testing.T) {
	if policy.Covers("/d", "/db/prod") {
		t.Fatal(`covers("/d","/db/prod") 应为 false`)
	}
	if !policy.Covers("/db", "/db/prod/users") || !policy.Covers("/db", "/db") {
		t.Fatal("covers 段边界错误")
	}
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(11, "g1", "m2"); err != ErrPermission {
		t.Fatalf("m2(/d) 评审 /db/prod = %v, want ErrPermission", err)
	}
	if err := svc.Approve(11, "g1", "m1"); err != nil {
		t.Fatalf("m1(/db) 评审 /db/prod = %v", err)
	}
}

// 驳回晚于 nominalEnd：effEnd=min(nominalEnd, rejectedAt)=nominalEnd。
func TestRejectAfterNominalEnd(t *testing.T) {
	svc, err := NewService(policy.Policy{Dmax: 100, Rw: 200, Lk: 50, Cmax: 3})
	if err != nil {
		t.Fatal(err)
	}
	svc.Register(0, "alice", policy.RoleEngineer, nil)
	svc.Register(0, "sec", policy.RoleSecurity, nil)
	if err := svc.Request(10, "g1", "alice", "/a", 50); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reject(70, "g1", "sec"); err != nil { // 70 > nominalEnd=60
		t.Fatal(err)
	}
	if got := svc.grants["g1"].EffEnd(); got != 60 {
		t.Fatalf("effEnd=%d, want 60(=nominalEnd)", got)
	}
	mustAccess(t, svc, "alice", "/a", 59, true)
	mustAccess(t, svc, "alice", "/a", 60, false)
}

// 无评审授权的锁定区间 [start+Rw, start+Rw+Lk)，恰等两端分别锁定/放行。
func TestLockBoundariesNoReview(t *testing.T) {
	svc, err := NewService(policy.Policy{Dmax: 100, Rw: 30, Lk: 50, Cmax: 5})
	if err != nil {
		t.Fatal(err)
	}
	svc.Register(0, "alice", policy.RoleEngineer, nil)
	svc.Register(0, "sec", policy.RoleSecurity, nil)
	if err := svc.Request(10, "g1", "alice", "/a", 10); err != nil {
		t.Fatal(err)
	}
	// lockStart=40, 锁定 [40,90)
	if err := svc.Request(39, "g2", "alice", "/a", 10); err != nil {
		t.Fatalf("request@39 = %v, want nil(尚未锁定)", err)
	}
	if err := svc.Approve(39, "g2", "sec"); err != nil { // 已批准者无锁定
		t.Fatal(err)
	}
	if err := svc.Request(40, "g3", "alice", "/a", 10); err != ErrLocked {
		t.Fatalf("request@40 = %v, want ErrLocked(恰等 lockStart)", err)
	}
	if err := svc.Request(89, "g3", "alice", "/a", 10); err != ErrLocked {
		t.Fatalf("request@89 = %v, want ErrLocked", err)
	}
	if err := svc.Request(90, "g3", "alice", "/a", 10); err != nil {
		t.Fatalf("request@90 = %v, want nil(恰等 lockStart+Lk)", err)
	}
}

// 自评优先于权限：申请人即使无评审权限也报自评错误。
func TestSelfReviewPriority(t *testing.T) {
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reject(11, "g1", "alice"); err != ErrSelfReview {
		t.Fatalf("self reject = %v, want ErrSelfReview(优先于权限)", err)
	}
	if err := svc.Approve(11, "g1", "ghost"); err != ErrPermission {
		t.Fatalf("未登记 approver = %v, want ErrPermission", err)
	}
}

// 历史可重复读：t 之后的评审(now>t)不改变 Access(·,·,t)。
func TestHistoryStableAcrossReview(t *testing.T) {
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.Register(39, "tmp", policy.RoleEngineer, nil); err != nil {
		t.Fatal(err)
	}
	probes := []int64{10, 15, 39}
	before := make([]bool, len(probes))
	for i, tp := range probes {
		before[i], _ = svc.Access("alice", "/db/prod", tp)
	}
	if err := svc.Approve(40, "g1", "m1"); err != nil { // now=40 > 全部 probe
		t.Fatal(err)
	}
	if err := svc.Request(41, "g2", "alice", "/db/prod", 5); err != nil {
		t.Fatal(err)
	}
	for i, tp := range probes {
		after, _ := svc.Access("alice", "/db/prod", tp)
		t.Logf("t=%d before=%v after=%v", tp, before[i], after)
		if after != before[i] {
			t.Fatalf("t=%d 历史答案被改写: %v -> %v", tp, before[i], after)
		}
	}
}

// 被拒绝的操作不写账本、不推进时钟；Ledger 返回副本；重放一致。
func TestLedgerIntegrity(t *testing.T) {
	svc := newExampleSvc(t)
	base := svc.Ledger(0)
	if len(base) != 4 {
		t.Fatalf("register 条目=%d, want 4", len(base))
	}
	svc.Request(10, "g1", "alice", "/db/prod", 100)
	n := svc.Ledger(0)
	// 以下全部失败，账本不得增长
	svc.Register(5, "alice", policy.RoleEngineer, nil)  // 时钟回退
	svc.Register(20, "alice", policy.RoleEngineer, nil) // 重复
	svc.Request(20, "g1", "alice", "/db/prod", 100)     // id 重复
	svc.Request(20, "gx", "alice", "/db/prod", 101)     // D 超 Dmax
	svc.Approve(20, "g1", "m2")                         // 权限
	if got := svc.Ledger(0); len(got) != len(n) {
		t.Fatalf("被拒绝操作写入账本: %d -> %d", len(n), len(got))
	}
	svc.Approve(20, "g1", "m1")
	entries := svc.Ledger(0)
	for i, e := range entries {
		if e.Seq != int64(i+1) {
			t.Fatalf("Seq 有洞: [%d].Seq=%d", i, e.Seq)
		}
	}
	t.Logf("账本: %+v", entries[len(entries)-1])
	if entries[len(entries)-1].Kind != ledger.KindApprove {
		t.Fatal("最后一条应为 approve")
	}
	// 副本语义：篡改返回值不影响账本
	entries[0].Seq = 999
	if svc.Ledger(0)[0].Seq != 1 {
		t.Fatal("Ledger 未返回副本")
	}
	// afterSeq 过滤
	if got := svc.Ledger(4); len(got) != len(entries)-4 || got[0].Seq != 5 {
		t.Fatalf("Ledger(4) 错误: %v", got)
	}
}
