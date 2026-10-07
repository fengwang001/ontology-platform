package audit_test

import (
	"errors"
	"testing"

	"ontology/audit"
)

func allowRule(id string, order int, subjects, targets, actions []string) audit.Rule {
	return audit.Rule{ID: id, Order: order, Subjects: subjects, Targets: targets, Actions: actions, Effect: audit.EffectAllow}
}

func denyRule(id string, order int, subjects, targets, actions []string) audit.Rule {
	return audit.Rule{ID: id, Order: order, Subjects: subjects, Targets: targets, Actions: actions, Effect: audit.EffectDeny}
}

func newSvc(t *testing.T) (*audit.Service, *audit.SliceLogger) {
	t.Helper()
	logger := audit.NewSliceLogger()
	return audit.NewService(logger), logger
}

func commitV(t *testing.T, svc *audit.Service, id, parent string, rules ...audit.Rule) audit.RuleVersion {
	t.Helper()
	v, err := svc.CommitVersion(audit.RuleVersionInput{ID: id, ParentID: parent, Rules: audit.RuleSet{Rules: rules}})
	if err != nil {
		t.Fatalf("CommitVersion %s: %v", id, err)
	}
	return v
}

func TestVersionChainIsAppendOnlyAndOrdered(t *testing.T) {
	svc, _ := newSvc(t)
	v1 := commitV(t, svc, "v1", "", allowRule("r1", 1, nil, nil, nil))
	if _, err := svc.CommitVersion(audit.RuleVersionInput{ID: "v1", ParentID: "v1"}); !errors.Is(err, audit.ErrAlreadyExists) {
		t.Fatalf("duplicate id: got %v", err)
	}
	if _, err := svc.CommitVersion(audit.RuleVersionInput{ID: "v2", ParentID: "nope"}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("bad parent: got %v", err)
	}
	v2 := commitV(t, svc, "v2", "v1", denyRule("r2", 1, nil, nil, nil))
	v3 := commitV(t, svc, "v3", "v2", allowRule("r3", 1, nil, nil, nil))
	if v1.Seq != 1 || v2.Seq != 2 || v3.Seq != 3 {
		t.Fatalf("seqs = %d,%d,%d", v1.Seq, v2.Seq, v3.Seq)
	}
	if v1.ContentHash == v2.ContentHash || v2.ChainHash == "" || v2.PrevChainHash != v1.ChainHash {
		t.Fatal("version chain not linked")
	}
	if problems := svc.VerifyAll(); len(problems) != 0 {
		t.Fatalf("clean chains reported problems: %v", problems)
	}
}

func TestReplayMatch(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", allowRule("r1", 1, []string{"alice"}, nil, nil))
	rec, err := svc.RecordAudit(audit.AuditInput{Subject: "alice", Target: "doc1", Action: "read", Content: `{"q":1}`, RuleVersionID: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Replay(rec.ID)
	if err != nil || rep.Outcome != audit.ReplayMatch || !rep.ReplayedAllow {
		t.Fatalf("replay = %+v err=%v", rep, err)
	}
}

func TestReplayMismatchDefectiveOriginal(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec, err := svc.RecordAuditWithEngine(
		audit.AuditInput{Subject: "alice", Target: "doc1", Action: "read", RuleVersionID: "v1"},
		audit.StaticEngine{Name: "buggy", Answer: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Replay(rec.ID)
	if err != nil {
		t.Fatalf("mismatch must not be an error: %v", err)
	}
	if rep.Outcome != audit.ReplayMismatchDefectiveOriginal || rep.ReplayedAllow || !rep.OriginalAllow {
		t.Fatalf("replay = %+v", rep)
	}
}

func TestReplayVersionTampered(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec, _ := svc.RecordAudit(audit.AuditInput{Subject: "alice", Target: "d", Action: "read", RuleVersionID: "v1"})
	if !svc.TamperVersion("v1", func(v *audit.RuleVersion) {
		v.Rules = audit.RuleSet{Rules: []audit.Rule{allowRule("r9", 9, nil, nil, nil)}}
	}) {
		t.Fatal("tamper failed")
	}
	rep, err := svc.Replay(rec.ID)
	if !errors.Is(err, audit.ErrVersionTampered) || rep.Outcome != audit.ReplayIntegrityVersionTampered {
		t.Fatalf("got outcome=%s err=%v", rep.Outcome, err)
	}
}

func TestReplayAuditTampered(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec, _ := svc.RecordAudit(audit.AuditInput{Subject: "alice", Target: "d", Action: "read", RuleVersionID: "v1"})
	svc.TamperAudit(rec.ID, func(a *audit.AuditRecord) { a.Allow = !a.Allow })
	rep, err := svc.Replay(rec.ID)
	if !errors.Is(err, audit.ErrAuditTampered) || rep.Outcome != audit.ReplayIntegrityAuditTampered {
		t.Fatalf("got outcome=%s err=%v", rep.Outcome, err)
	}
}

func TestReplayVersionMissing(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", allowRule("r1", 1, nil, nil, nil))
	rec, _ := svc.RecordAudit(audit.AuditInput{Subject: "alice", Target: "d", Action: "read", RuleVersionID: "v1"})
	if !svc.DeleteVersion("v1") {
		t.Fatal("delete failed")
	}
	_, err := svc.Replay(rec.ID)
	if !errors.Is(err, audit.ErrVersionNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestReplayAuditMissing(t *testing.T) {
	svc, _ := newSvc(t)
	if _, err := svc.Replay("audit-404"); !errors.Is(err, audit.ErrAuditNotFound) {
		t.Fatalf("got %v", err)
	}
}
