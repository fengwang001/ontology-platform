package ontology

import "testing"

// 每次校验都必须记录输入（参数/目标）、校验依据（读到的对象版本）
// 与结论（各条件结果与整体结论），供事后核对。
func TestValidationRecordsCaptureInputBasisAndConclusion(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	mustRegister(t, reg, transferAction())
	mustRegister(t, reg, spendAction(false))
	seedAccounts(store, 2, 100)
	exec := NewExecutor(store, reg)
	// 一次全通过（产生前置+后置两条记录）。
	ok := exec.Execute(transferCall("ok-1", accountID(0), accountID(1), 40))
	if !ok.Accepted() {
		t.Fatalf("setup: %v", ok.Reject)
	}
	// 一次前置失败。
	exec.Execute(transferCall("bad-pre", accountID(0), accountID(1), -5))
	// 一次后置失败。
	exec.Execute(spendCall("bad-post", accountID(0), 500))
	log := store.ValidationLog()
	// ok-1: pre+post；bad-pre: pre；bad-post: pre+post → 共 5 条。
	if len(log) != 5 {
		t.Fatalf("validation log length = %d, want 5", len(log))
	}
	// 审计序号连续且独立于对象版本。
	for i, rec := range log {
		if rec.Seq != int64(i+1) {
			t.Fatalf("audit seq not contiguous at %d: %+v", i, rec)
		}
		if rec.CallID == "" || rec.ActionType == "" {
			t.Fatalf("record missing identity: %+v", rec)
		}
		if rec.Params == nil || len(rec.Targets) == 0 {
			t.Fatalf("record missing input: %+v", rec)
		}
		if len(rec.Outcomes) == 0 {
			t.Fatalf("record missing conclusions: %+v", rec)
		}
	}
	// ok-1 的前置记录：依据必须包含两个账户当时的版本（均为 0）。
	var okPre *ValidationRecord
	for i := range log {
		if log[i].CallID == "ok-1" && log[i].Phase == PhasePre {
			okPre = &log[i]
		}
	}
	if okPre == nil || !okPre.PassedAll {
		t.Fatalf("missing passed pre record for ok-1")
	}
	for _, id := range []ObjectID{accountID(0), accountID(1)} {
		ver, ok := okPre.Basis.ObjectVersions[id]
		if !ok || ver != 0 {
			t.Fatalf("basis for %s = (%d,%v), want (0,true)", id, ver, ok)
		}
	}
	if okPre.Params["amount"].(int64) != 40 {
		t.Fatalf("input params not recorded: %+v", okPre.Params)
	}
	// bad-pre 的前置记录：结论为不通过，且列出全部条件结果。
	var badPre *ValidationRecord
	for i := range log {
		if log[i].CallID == "bad-pre" {
			badPre = &log[i]
		}
	}
	if badPre == nil || badPre.PassedAll || len(badPre.Outcomes) != 2 {
		t.Fatalf("bad-pre record malformed: %+v", badPre)
	}
	// bad-post 的后置记录：结论为不通过，决定性条件在结果末尾。
	var badPost *ValidationRecord
	for i := range log {
		if log[i].CallID == "bad-post" && log[i].Phase == PhasePost {
			badPost = &log[i]
		}
	}
	if badPost == nil || badPost.PassedAll {
		t.Fatalf("missing failed post record for bad-post")
	}
	last := badPost.Outcomes[len(badPost.Outcomes)-1]
	if last.Passed || last.ID != "no-negative-balance" {
		t.Fatalf("decisive condition not recorded: %+v", badPost.Outcomes)
	}
	// 失败轨迹与校验记录可互相引用。
	trail := store.FailureTrail()
	if len(trail) != 1 || trail[0].CallID != "bad-post" {
		t.Fatalf("failure trail = %+v, want single bad-post entry", trail)
	}
	if len(trail[0].ValidationSeqs) != 2 {
		t.Fatalf("failure record must reference pre+post validation seqs: %+v", trail[0])
	}
}
