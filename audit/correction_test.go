package audit_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"ontology/audit"
)

func setupMisjudgedRecord(t *testing.T) (*audit.System, audit.RecordID) {
	t.Helper()
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	// 规则允许，但原判定记录为拒绝：一次历史误判。
	rec, err := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionDeny)
	if err != nil {
		t.Fatal(err)
	}
	return s, rec
}

// 追加纠正后三态查询返回"已被纠正"，原始记录不被修改。
func TestCorrectionChangesLegality(t *testing.T) {
	s, rec := setupMisjudgedRecord(t)
	before, _ := s.GetRecord(rec)

	if _, err := s.AppendCorrection(rec, "", audit.DecisionAllow, "PDP cache staleness caused false deny"); err != nil {
		t.Fatal(err)
	}

	leg, eff, err := s.EffectiveDecision(rec)
	if err != nil {
		t.Fatal(err)
	}
	if leg != audit.LegalityCorrected || eff != audit.DecisionAllow {
		t.Fatalf("want CORRECTED/ALLOW, got %v/%v", leg, eff)
	}

	after, _ := s.GetRecord(rec)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("original record mutated: %+v -> %+v", before, after)
	}
}

// 多条纠正记录形成确定顺序的链，链头结论为有效结论。
func TestCorrectionChainOverrides(t *testing.T) {
	s, rec := setupMisjudgedRecord(t)

	c1, err := s.AppendCorrection(rec, "", audit.DecisionAllow, "first correction")
	if err != nil {
		t.Fatal(err)
	}
	// 第二条纠正必须指向链头。
	if _, err := s.AppendCorrection(rec, "", audit.DecisionDeny, "orphan"); !errors.Is(err, audit.ErrCorrectionChain) {
		t.Fatalf("non-head append must be rejected, got %v", err)
	}
	c2, err := s.AppendCorrection(rec, c1, audit.DecisionDeny, "re-review: deny after all")
	if err != nil {
		t.Fatal(err)
	}
	c3, err := s.AppendCorrection(rec, c2, audit.DecisionAllow, "final ruling: allow")
	if err != nil {
		t.Fatal(err)
	}

	chain, err := s.CorrectionsOf(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 || chain[0].ID != c1 || chain[1].ID != c2 || chain[2].ID != c3 {
		t.Fatalf("correction chain order not deterministic: %+v", chain)
	}
	if chain[1].Supersedes != c1 || chain[2].Supersedes != c2 {
		t.Fatalf("supersedes links wrong: %+v", chain)
	}

	leg, eff, _ := s.EffectiveDecision(rec)
	if leg != audit.LegalityCorrected || eff != audit.DecisionAllow {
		t.Fatalf("chain head must win: got %v/%v", leg, eff)
	}
}

// 链头结论与原始记录一致时，回答回到"依据原始记录"。
func TestCorrectionReaffirmingOriginal(t *testing.T) {
	s, rec := setupMisjudgedRecord(t)
	c1, _ := s.AppendCorrection(rec, "", audit.DecisionAllow, "correct to allow")
	if _, err := s.AppendCorrection(rec, c1, audit.DecisionDeny, "on reflection original deny was right"); err != nil {
		t.Fatal(err)
	}
	leg, eff, _ := s.EffectiveDecision(rec)
	if leg != audit.LegalityDenyAsRecorded || eff != audit.DecisionDeny {
		t.Fatalf("reaffirmed original must read as DENY_AS_RECORDED, got %v/%v", leg, eff)
	}
}

// 三态查询：合法 / 不合法 / 已被纠正，且与查询发出的时刻无关。
func TestQueryLegalityThreeStates(t *testing.T) {
	s := audit.New()
	vAllow, _ := s.SubmitRuleVersion(allowAliceRules())
	vDeny, _ := s.SubmitRuleVersion(denyAllRules())

	t1, t2, t3 := t0, t0.Add(time.Hour), t0.Add(2*time.Hour)
	s.RecordAccess("alice", "doc-1", readReq(), t1, vAllow, audit.DecisionAllow)
	s.RecordAccess("bob", "doc-1", readReq(), t2, vDeny, audit.DecisionDeny)
	misRec, _ := s.RecordAccess("carol", "doc-1", readReq(), t3, vAllow, audit.DecisionDeny)
	s.AppendCorrection(misRec, "", audit.DecisionAllow, "misjudgment")

	cases := []struct {
		subject string
		at      time.Time
		want    audit.Legality
	}{
		{"alice", t1, audit.LegalityAllowAsRecorded},
		{"bob", t2, audit.LegalityDenyAsRecorded},
		{"carol", t3, audit.LegalityCorrected},
	}
	for _, tc := range cases {
		leg, rec, _, err := s.QueryLegality(tc.subject, "doc-1", tc.at)
		if err != nil {
			t.Fatal(err)
		}
		if leg != tc.want {
			t.Fatalf("%s: want %v, got %v", tc.subject, tc.want, leg)
		}
		if rec.Subject != tc.subject {
			t.Fatalf("%s: wrong record returned", tc.subject)
		}
	}

	// 查询结果不依赖于查询发出的时刻：任意时间重复查询结果一致。
	for i := 0; i < 3; i++ {
		leg, _, _, err := s.QueryLegality("carol", "doc-1", t3)
		if err != nil || leg != audit.LegalityCorrected {
			t.Fatalf("query result drifted over time: %v %v", leg, err)
		}
	}
}

// 纠正指向不存在的原始记录 → 拒绝且不产生任何影响。
func TestCorrectionTargetMissing(t *testing.T) {
	s, rec := setupMisjudgedRecord(t)
	if _, err := s.AppendCorrection("ar-999", "", audit.DecisionAllow, "dangling"); !errors.Is(err, audit.ErrCorrectionTargetMissing) {
		t.Fatalf("want ErrCorrectionTargetMissing, got %v", err)
	}
	// 被拒绝的追加不影响既有状态。
	chain, _ := s.CorrectionsOf(rec)
	if len(chain) != 0 {
		t.Fatalf("rejected correction had an effect: %+v", chain)
	}
	if _, _, err := s.EffectiveDecision(rec); err != nil {
		t.Fatal(err)
	}
}

// 纠正指向其他记录的纠正链 → 拒绝。
func TestCorrectionCrossRecordSupersedes(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	r1, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionDeny)
	r2, _ := s.RecordAccess("alice", "doc-1", readReq(), t0.Add(time.Hour), v, audit.DecisionDeny)
	c1, _ := s.AppendCorrection(r1, "", audit.DecisionAllow, "fix r1")
	if _, err := s.AppendCorrection(r2, c1, audit.DecisionAllow, "cross-record"); !errors.Is(err, audit.ErrCorrectionChain) {
		t.Fatalf("cross-record supersedes must be rejected, got %v", err)
	}
}

// 指向不存在的纠正记录 → 拒绝。
func TestCorrectionSupersedeMissing(t *testing.T) {
	s, rec := setupMisjudgedRecord(t)
	if _, err := s.AppendCorrection(rec, "cr-999", audit.DecisionAllow, "bad ref"); !errors.Is(err, audit.ErrCorrectionNotFound) {
		t.Fatalf("want ErrCorrectionNotFound, got %v", err)
	}
}

// 错误优先级固定且唯一。
func TestErrorPriorityOrder(t *testing.T) {
	if !(audit.ErrorPriority(audit.ErrVersionNotFound) < audit.ErrorPriority(audit.ErrAuditIntegrity)) {
		t.Fatal("version-not-found must outrank audit-integrity")
	}
	if !(audit.ErrorPriority(audit.ErrAuditIntegrity) < audit.ErrorPriority(audit.ErrCorrectionTargetMissing)) {
		t.Fatal("audit-integrity must outrank correction-target-missing")
	}
}
