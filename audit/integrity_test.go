package audit_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/audit"
)

// 组合 1：仅版本内容被篡改 → 回放报告 VERSION_TAMPERED。
func TestIntegrityVersionContentOnly(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.UnsafeCorruptVersionContent(v, func(rs *audit.RuleSet) {
		rs.Rules = append(rs.Rules, audit.Rule{Effect: audit.EffectDeny, Subjects: []string{"*"}, Targets: []string{"*"}, Actions: []string{"*"}})
	})
	res, _, err := s.Replay(rec)
	if err != nil || res.Outcome != audit.ReplayVersionTampered {
		t.Fatalf("got %v err=%v", res, err)
	}
}

// 组合 2：仅记录结果被篡改 → ErrAuditIntegrity。
func TestIntegrityRecordResultOnly(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.UnsafeCorruptRecordResult(rec, audit.DecisionDeny)
	_, _, err := s.Replay(rec)
	if !errors.Is(err, audit.ErrAuditIntegrity) {
		t.Fatalf("want ErrAuditIntegrity, got %v", err)
	}
}

// 组合 3：记录登记的版本标识被篡改为不存在的版本 → ErrVersionNotFound（优先级 1）。
func TestIntegrityRecordVersionRefDangling(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.UnsafeCorruptRecordVersionRef(rec, "rv-999")
	_, _, err := s.Replay(rec)
	if !errors.Is(err, audit.ErrVersionNotFound) {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
}

// 组合 4：记录登记的版本标识被篡改为另一个存在的版本 → 记录自哈希破坏 → ErrAuditIntegrity。
func TestIntegrityRecordVersionRefSwapped(t *testing.T) {
	s := audit.New()
	v1, _ := s.SubmitRuleVersion(allowAliceRules())
	v2, _ := s.SubmitRuleVersion(denyAllRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v1, audit.DecisionAllow)
	s.UnsafeCorruptRecordVersionRef(rec, v2)
	_, _, err := s.Replay(rec)
	if !errors.Is(err, audit.ErrAuditIntegrity) {
		t.Fatalf("want ErrAuditIntegrity, got %v", err)
	}
}

// 组合 5：记录链哈希被篡改 → ErrAuditIntegrity。
func TestIntegrityRecordChainHash(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.UnsafeCorruptRecordHash(rec, "deadbeef")
	_, _, err := s.Replay(rec)
	if !errors.Is(err, audit.ErrAuditIntegrity) {
		t.Fatalf("want ErrAuditIntegrity, got %v", err)
	}
}

// 组合 6：版本内容与记录结果同时被篡改 → 按固定优先级报 ErrAuditIntegrity。
func TestIntegrityBothVersionAndRecord(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.UnsafeCorruptVersionContent(v, func(rs *audit.RuleSet) { rs.Rules[0].Effect = audit.EffectDeny })
	s.UnsafeCorruptRecordResult(rec, audit.DecisionDeny)
	_, _, err := s.Replay(rec)
	if !errors.Is(err, audit.ErrAuditIntegrity) {
		t.Fatalf("record integrity must outrank version tampering, got %v", err)
	}
}

// 组合 7：版本被删除且记录被篡改 → 按固定优先级报 ErrVersionNotFound。
func TestIntegrityDeletedVersionAndCorruptRecord(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.UnsafeDeleteVersion(v)
	s.UnsafeCorruptRecordResult(rec, audit.DecisionDeny)
	_, _, err := s.Replay(rec)
	if !errors.Is(err, audit.ErrVersionNotFound) {
		t.Fatalf("version-not-found must outrank audit-integrity, got %v", err)
	}
}

// 组合 8：篡改一个版本不影响引用其他完好版本的记录回放。
func TestIntegrityTamperIsolated(t *testing.T) {
	s := audit.New()
	v1, _ := s.SubmitRuleVersion(allowAliceRules())
	v2, _ := s.SubmitRuleVersion(denyAllRules())
	rec1, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v1, audit.DecisionAllow)
	rec2, _ := s.RecordAccess("bob", "doc-1", readReq(), t0, v2, audit.DecisionDeny)
	s.UnsafeCorruptVersionContent(v1, func(rs *audit.RuleSet) { rs.Rules[0].Effect = audit.EffectDeny })

	res1, _, _ := s.Replay(rec1)
	if res1.Outcome != audit.ReplayVersionTampered {
		t.Fatalf("rec1: want VERSION_TAMPERED, got %v", res1)
	}
	res2, _, err := s.Replay(rec2)
	if err != nil || res2.Outcome != audit.ReplayConsistent {
		t.Fatalf("rec2 on intact version must be unaffected: %v err=%v", res2, err)
	}
}

// 被拒绝的回放与纠正不修改任何既有记录或版本。
func TestRejectedOpsHaveNoEffect(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	beforeRec, _ := s.GetRecord(rec)
	beforeVer, _ := s.GetVersion(v)

	s.Replay("ar-999")                                                             // 拒绝：记录不存在
	s.AppendCorrection("ar-999", "", audit.DecisionDeny, "x")                      // 拒绝：目标不存在
	s.RecordAccess("alice", "doc-1", readReq(), t0, "rv-999", audit.DecisionAllow) // 拒绝：版本不存在

	afterRec, _ := s.GetRecord(rec)
	afterVer, _ := s.GetVersion(v)
	if !reflect.DeepEqual(beforeRec, afterRec) {
		t.Fatal("rejected ops mutated an audit record")
	}
	if beforeVer.ChainHash != afterVer.ChainHash || beforeVer.ContentHash != afterVer.ContentHash {
		t.Fatal("rejected ops mutated a rule version")
	}
	if _, err := s.GetRecord("ar-2"); !errors.Is(err, audit.ErrRecordNotFound) {
		t.Fatal("rejected ops created new records")
	}
	if _, err := s.GetVersion("rv-2"); !errors.Is(err, audit.ErrVersionNotFound) {
		t.Fatal("rejected ops created new versions")
	}
}

// 返回的记录/版本为深拷贝：调用方无法借此绕过不可变性。
func TestReturnedCopiesAreIsolated(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)

	gv, _ := s.GetVersion(v)
	gv.Content.Rules[0].Effect = audit.EffectDeny
	gr, _ := s.GetRecord(rec)
	gr.Result = audit.DecisionDeny

	res, _, err := s.Replay(rec)
	if err != nil || res.Outcome != audit.ReplayConsistent {
		t.Fatalf("caller-side mutation leaked into store: %v err=%v", res, err)
	}
}
