package runner_test

import (
	"testing"

	"ontology/grants"
	"ontology/runner"
)

// 到期恰等与差 1：deadline=now+T，恰等即到期，差 1 仍可放行。
func TestExpiryBoundary(t *testing.T) {
	// 差 1：deadline=11，now=10 未到期，Approve 成功。
	r, g, c := newKit(t, 10)
	setupDenied(t, r, g, c)
	if err := g.Grant([]byte("a"), grants.ApproverBit); err != nil {
		t.Fatal(err)
	}
	if err := r.Approve([]byte("i"), []byte("a"), 10); err != nil {
		t.Fatalf("deadline-1 应仍可 Approve: %v", err)
	}
	t.Logf("Approve@10（deadline=11，差1）→ 成功；判定依据 10<11 未到期")

	// 恰等：deadline=11，Status(11) 虚拟 Failed(Expired)，不落库。
	r2, g2, c2 := newKit(t, 10)
	setupDenied(t, r2, g2, c2)
	st, err := r2.Status([]byte("i"), 11)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != runner.FailedPhase || st.Outcome != runner.Expired || st.TerminalAt != 11 {
		t.Fatalf("Status@11 应虚拟到期: %+v", st)
	}
	t.Logf("Status@11 → %s@%d（只读虚拟，恰等到期）", st.Outcome, st.TerminalAt)
	evs, _ := r2.AuditLog([]byte("i"))
	if len(evs) != 1 || kinds(evs)[0] != "Deny" {
		t.Fatalf("只读 Status 不得落库 Expire: %v", kinds(evs))
	}
	// 虚拟到期后再 Status@10 非法（时钟）；Status@10 用另一个实例验证时钟外，
	// 这里直接 Approve@11：ErrState 先于 ErrSelf/ErrNoAuthority，且 Expire 落库。
	err = r2.Approve([]byte("i"), []byte("p"), 11) // 自批，本应 ErrSelf
	requireErrIs(t, err, runner.ErrState, "恰等到期时自批")
	evs, _ = r2.AuditLog([]byte("i"))
	if len(evs) != 2 || kinds(evs)[1] != "Expire" || evs[1].At != 11 || evs[1].Seq != 2 {
		t.Fatalf("到期应在拒绝调用中落库 Expire@11: %+v", evs)
	}
	t.Logf("Approve(自批)@11 → ErrState 且审计补 Expire@11；到期是 now 的纯函数")
	st, _ = r2.Status([]byte("i"), 11)
	if st.Phase != runner.FailedPhase {
		t.Fatalf("落库后应终局: %+v", st)
	}
	// 终局唯一且之后不变：再来 Approve 仍是 ErrState，审计不再增长。
	err = r2.Reject([]byte("i"), []byte("a"), 12)
	requireErrIs(t, err, runner.ErrState, "终局后 Reject")
	evs, _ = r2.AuditLog([]byte("i"))
	if len(evs) != 2 {
		t.Fatalf("终局后审计不得增长: %v", kinds(evs))
	}
}

// Approve 与到期同刻时以到期为准（恰等即到期）。
func TestApproveAtDeadline(t *testing.T) {
	r, g, c := newKit(t, 5)
	setupDenied(t, r, g, c)
	if err := g.Grant([]byte("a"), grants.ApproverBit); err != nil {
		t.Fatal(err)
	}
	// StartStep@1 Deny → deadline=6。
	err := r.Approve([]byte("i"), []byte("a"), 6)
	requireErrIs(t, err, runner.ErrState, "Approve 与到期同刻")
	st, _ := r.Status([]byte("i"), 6)
	if st.Outcome != runner.Expired {
		t.Fatalf("同刻应判 Expired: %+v", st)
	}
	t.Logf("Approve@6==deadline → Expired 优先；判定依据 deadline≤now 即到期")
}

// Reject 成功路径：Failed(Rejected)，终局时刻 now。
func TestRejectOutcome(t *testing.T) {
	r, g, c := newKit(t, 10)
	setupDenied(t, r, g, c)
	if err := g.Grant([]byte("a"), grants.ApproverBit); err != nil {
		t.Fatal(err)
	}
	if err := r.Reject([]byte("i"), []byte("a"), 4); err != nil {
		t.Fatal(err)
	}
	st, _ := r.Status([]byte("i"), 4)
	if st.Phase != runner.FailedPhase || st.Outcome != runner.Rejected || st.TerminalAt != 4 {
		t.Fatalf("Reject 终局错误: %+v", st)
	}
	evs, _ := r.AuditLog([]byte("i"))
	if kinds(evs)[1] != "Reject" {
		t.Fatalf("审计应为 [Deny Reject]: %v", kinds(evs))
	}
	if string(evs[1].Approver) != "a" {
		t.Fatalf("Reject 审计审批人 = %q", evs[1].Approver)
	}
	t.Logf("Reject@4 → %s@4，审计 %v", st.Outcome, kinds(evs))
}
