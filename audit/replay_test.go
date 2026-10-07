package audit_test

import (
	"errors"
	"testing"
	"time"

	"ontology/audit"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func allowAliceRules() audit.RuleSet {
	return audit.RuleSet{Rules: []audit.Rule{
		{Effect: audit.EffectAllow, Subjects: []string{"alice"}, Targets: []string{"doc-1"}, Actions: []string{"read"}},
	}}
}

func denyAllRules() audit.RuleSet {
	return audit.RuleSet{Rules: []audit.Rule{
		{Effect: audit.EffectDeny, Subjects: []string{"*"}, Targets: []string{"*"}, Actions: []string{"*"}},
	}}
}

func readReq() audit.Request { return audit.Request{Action: "read"} }

// 回放结果与原记录一致。
func TestReplayConsistent(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, err := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	if err != nil {
		t.Fatal(err)
	}
	res, stats, err := s.Replay(rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != audit.ReplayConsistent {
		t.Fatalf("want CONSISTENT, got %v", res)
	}
	if res.Recomputed != audit.DecisionAllow || res.Original != audit.DecisionAllow {
		t.Fatalf("unexpected decisions: %v", res)
	}
	if stats.VersionsScanned != 1 || stats.RecordsScanned != 1 {
		t.Fatalf("replay must read exactly one version and one record, got %+v", stats)
	}
}

// 原判定过程存在缺陷（记录的结果与规则内容不符）→ 历史误判。
func TestReplayMisjudgment(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	// 模拟有缺陷的判定路径：规则允许，但记录为拒绝。
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionDeny)
	res, _, err := s.Replay(rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != audit.ReplayMisjudgment {
		t.Fatalf("want MISJUDGMENT, got %v", res)
	}
	if res.Recomputed != audit.DecisionAllow || res.Original != audit.DecisionDeny {
		t.Fatalf("unexpected decisions: %v", res)
	}
}

// 版本规则内容被篡改 → 完整性破坏，以可区分的结论报告。
func TestReplayVersionTampered(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	if err := s.UnsafeCorruptVersionContent(v, func(rs *audit.RuleSet) {
		rs.Rules[0].Effect = audit.EffectDeny
	}); err != nil {
		t.Fatal(err)
	}
	res, _, err := s.Replay(rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != audit.ReplayVersionTampered {
		t.Fatalf("want VERSION_TAMPERED, got %v", res)
	}
}

// 登记的版本标识不存在 → 优先级最高的错误。
func TestReplayVersionNotFound(t *testing.T) {
	s := audit.New()
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.UnsafeDeleteVersion(v)
	_, _, err := s.Replay(rec)
	if !errors.Is(err, audit.ErrVersionNotFound) {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
}

// 回放不存在的记录。
func TestReplayRecordNotFound(t *testing.T) {
	s := audit.New()
	_, _, err := s.Replay("ar-999")
	if !errors.Is(err, audit.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

// 登记不存在的版本标识时，RecordAccess 被拒绝且不产生记录。
func TestRecordAccessUnknownVersionRejected(t *testing.T) {
	s := audit.New()
	_, err := s.RecordAccess("alice", "doc-1", readReq(), t0, "rv-404", audit.DecisionAllow)
	if !errors.Is(err, audit.ErrVersionNotFound) {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
	if _, err := s.GetRecord("ar-1"); !errors.Is(err, audit.ErrRecordNotFound) {
		t.Fatalf("rejected RecordAccess must not create a record, got %v", err)
	}
}

// 旧版本完整保留：提交新版本后，旧版本仍可回放且结论不变。
func TestOldVersionsPreserved(t *testing.T) {
	s := audit.New()
	v1, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v1, audit.DecisionAllow)
	// 后续多次变更不影响旧版本。
	s.SubmitRuleVersion(denyAllRules())
	s.SubmitRuleVersion(allowAliceRules())
	res, _, err := s.Replay(rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != audit.ReplayConsistent || res.Recomputed != audit.DecisionAllow {
		t.Fatalf("old version must remain replayable: %v", res)
	}
	gv, err := s.GetVersion(v1)
	if err != nil {
		t.Fatal(err)
	}
	if len(gv.Content.Rules) != 1 || gv.Content.Rules[0].Effect != audit.EffectAllow {
		t.Fatalf("old version content changed: %+v", gv.Content)
	}
}

// 版本顺序唯一且确定：ID 与序号单调递增。
func TestVersionOrderDeterministic(t *testing.T) {
	s := audit.New()
	var ids []audit.VersionID
	for i := 0; i < 5; i++ {
		id, _ := s.SubmitRuleVersion(denyAllRules())
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("version IDs not strictly increasing: %v", ids)
		}
	}
}

// 回放的版本读取开销与系统中版本总数无关（可观测证明）。
func TestReplayCostIndependentOfVersionCount(t *testing.T) {
	var statsSmall, statsLarge audit.ReadStats
	for _, n := range []int{1, 300} {
		s := audit.New()
		v1, _ := s.SubmitRuleVersion(allowAliceRules())
		rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v1, audit.DecisionAllow)
		for i := 1; i < n; i++ {
			s.SubmitRuleVersion(denyAllRules())
		}
		_, stats, err := s.Replay(rec)
		if err != nil {
			t.Fatal(err)
		}
		if stats.VersionsScanned != 1 {
			t.Fatalf("n=%d: replay scanned %d versions", n, stats.VersionsScanned)
		}
		if n == 1 {
			statsSmall = stats
		} else {
			statsLarge = stats
		}
	}
	if statsSmall.VersionBytesRead != statsLarge.VersionBytesRead {
		t.Fatalf("bytes read grew with version count: %d -> %d",
			statsSmall.VersionBytesRead, statsLarge.VersionBytesRead)
	}
	want := len(audit.Canonical(allowAliceRules()))
	if statsLarge.VersionBytesRead != want {
		t.Fatalf("bytes read %d != size of the single referenced version %d",
			statsLarge.VersionBytesRead, want)
	}
}

// 每次调用都有完整日志：输入、输出、裁决依据。
func TestCallLoggingComplete(t *testing.T) {
	ml := audit.NewMemoryLogger()
	s := audit.New(audit.WithLogger(ml))
	v, _ := s.SubmitRuleVersion(allowAliceRules())
	rec, _ := s.RecordAccess("alice", "doc-1", readReq(), t0, v, audit.DecisionAllow)
	s.Replay(rec)
	s.QueryLegality("alice", "doc-1", t0)

	ops := map[string]bool{}
	for _, e := range ml.Events() {
		ops[e.Op] = true
		if e.Input == nil {
			t.Fatalf("op %s missing logged input", e.Op)
		}
		if e.At.IsZero() {
			t.Fatalf("op %s missing timestamp", e.Op)
		}
	}
	for _, want := range []string{"submit_version", "record_access", "replay", "query_legality"} {
		if !ops[want] {
			t.Fatalf("missing log event for op %s", want)
		}
	}
}
