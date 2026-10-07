package audit_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/audit"
)

func defectiveAudit(t *testing.T, svc *audit.Service, version string) audit.AuditRecord {
	t.Helper()
	rec, err := svc.RecordAuditWithEngine(
		audit.AuditInput{Subject: "alice", Target: "d", Action: "read", RuleVersionID: version},
		audit.StaticEngine{Name: "buggy", Answer: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestCorrectionChainAndLegality(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec := defectiveAudit(t, svc, "v1")

	view0, err := svc.Legality(rec.ID)
	if err != nil || view0.Kind != audit.LegalityOriginalAllowed {
		t.Fatalf("view0=%+v err=%v", view0, err)
	}

	_, rep, err := svc.AppendCorrection(audit.AppendCorrectionInput{
		ID: "c1", AuditID: rec.ID, CorrectedAllow: false, Reason: "engine defect",
	})
	if err != nil || rep.Outcome != audit.ReplayMismatchDefectiveOriginal {
		t.Fatalf("append c1: err=%v rep=%+v", err, rep)
	}
	view1, _ := svc.Legality(rec.ID)
	if view1.Kind != audit.LegalityCorrected || view1.EffectiveAllow || view1.ActiveCorrection.ID != "c1" {
		t.Fatalf("view1=%+v", view1)
	}

	// Same conclusion again is rejected.
	if _, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{ID: "c1b", AuditID: rec.ID, CorrectedAllow: false}); !errors.Is(err, audit.ErrCorrectionConflict) {
		t.Fatalf("dup conclusion: %v", err)
	}
	// A later correction may overturn the previous correction.
	c2, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{ID: "c2", AuditID: rec.ID, CorrectedAllow: true, Reason: "re-reviewed"})
	if err != nil {
		t.Fatalf("append c2: %v", err)
	}
	if c2.TargetType != audit.CorrectionTargetCorrection || c2.TargetID != "c1" || c2.Seq != 2 {
		t.Fatalf("c2 target = %s/%s seq=%d", c2.TargetType, c2.TargetID, c2.Seq)
	}
	view2, _ := svc.Legality(rec.ID)
	// Effective conclusion is back to the original: the three-way answer is
	// ORIGINAL_ALLOWED, yet the negated c1 remains an immutable record.
	if view2.Kind != audit.LegalityOriginalAllowed || view2.ActiveCorrection.ID != "c2" {
		t.Fatalf("view2=%+v", view2)
	}

	// Original record untouched.
	got, _ := svc.GetAudit(rec.ID)
	if !got.Allow || got.RecordHash != rec.RecordHash {
		t.Fatal("original audit was modified")
	}
	list, _ := svc.ListCorrections(rec.ID)
	if len(list) != 2 {
		t.Fatalf("corrections = %d", len(list))
	}
}

func TestAppendCorrectionOnMatchRejected(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec, _ := svc.RecordAudit(audit.AuditInput{Subject: "alice", Target: "d", Action: "read", RuleVersionID: "v1"})
	if _, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{ID: "c1", AuditID: rec.ID, CorrectedAllow: true}); !errors.Is(err, audit.ErrReplayMatch) {
		t.Fatalf("got %v", err)
	}
}

func TestAppendCorrectionMissingTargets(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec := defectiveAudit(t, svc, "v1")
	_, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{
		ID: "c1", AuditID: rec.ID, CorrectedAllow: false,
		TargetType: audit.CorrectionTargetCorrection, TargetID: "ghost",
	})
	if !errors.Is(err, audit.ErrCorrectionTargetMissing) {
		t.Fatalf("ghost correction target: %v", err)
	}
	_, _, err = svc.AppendCorrection(audit.AppendCorrectionInput{
		ID: "c1", AuditID: "ghost", CorrectedAllow: false,
	})
	if !errors.Is(err, audit.ErrAuditNotFound) {
		t.Fatalf("ghost audit target: %v", err)
	}
	// Correction pointing at an audit record of a different audit.
	other := defectiveAudit(t, svc, "v1")
	if _, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{
		ID: "c1", AuditID: rec.ID, CorrectedAllow: false,
		TargetType: audit.CorrectionTargetAudit, TargetID: other.ID,
	}); !errors.Is(err, audit.ErrCorrectionConflict) {
		t.Fatalf("foreign audit target: %v", err)
	}
}

func TestRejectedAppendChangesNothing(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec, _ := svc.RecordAudit(audit.AuditInput{Subject: "alice", Target: "d", Action: "read", RuleVersionID: "v1"})
	before := svc.ListAudits()
	_, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{ID: "c1", AuditID: rec.ID, CorrectedAllow: true})
	if err == nil {
		t.Fatal("expected rejection")
	}
	after := svc.ListAudits()
	if len(before) != len(after) || before[0].RecordHash != after[0].RecordHash {
		t.Fatal("rejected append changed state")
	}
	if problems := svc.VerifyAll(); len(problems) != 0 {
		t.Fatalf("state corrupted: %v", problems)
	}
}

func TestErrorPrecedenceUniqueAndOrdered(t *testing.T) {
	seen := map[int]error{}
	for sentinel, p := range audit.ErrorPrecedence {
		if other, dup := seen[p]; dup {
			t.Fatalf("precedence %d shared by %v and %v", p, other, sentinel)
		}
		seen[p] = sentinel
	}
	order := []error{
		audit.ErrVersionNotFound,
		audit.ErrAuditTampered,
		audit.ErrCorrectionTargetMissing,
	}
	for i := 0; i+1 < len(order); i++ {
		if !(audit.ErrorPrecedence[order[i]] < audit.ErrorPrecedence[order[i+1]]) {
			t.Fatalf("%v must precede %v", order[i], order[i+1])
		}
	}
}

func TestIntegrityCombinationBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		tamperAudit bool
		tamperVer   bool
		deleteVer   bool
		want        error
	}{
		{"none", false, false, false, nil},
		{"audit only", true, false, false, audit.ErrAuditTampered},
		{"version only", false, true, false, audit.ErrVersionTampered},
		{"both tampered", true, true, false, audit.ErrVersionTampered},
		{"missing version", false, false, true, audit.ErrVersionNotFound},
		{"missing version and audit", true, false, true, audit.ErrVersionNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newSvc(t)
			commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
			rec, _ := svc.RecordAudit(audit.AuditInput{Subject: "alice", Target: "d", Action: "read", RuleVersionID: "v1"})
			if tc.tamperAudit {
				svc.TamperAudit(rec.ID, func(a *audit.AuditRecord) { a.Subject = "mallory" })
			}
			if tc.tamperVer {
				svc.TamperVersion("v1", func(v *audit.RuleVersion) { v.Rules = audit.RuleSet{} })
			}
			if tc.deleteVer {
				svc.DeleteVersion("v1")
			}
			_, err := svc.Replay(rec.ID)
			if tc.want == nil && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
		})
	}
}

func TestTamperedCorrectionDetected(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec := defectiveAudit(t, svc, "v1")
	c, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{ID: "c1", AuditID: rec.ID, CorrectedAllow: false})
	if err != nil {
		t.Fatal(err)
	}
	svc.TamperCorrection(c.ID, func(x *audit.Correction) { x.Reason = "rewritten" })
	if _, err := svc.Legality(rec.ID); !errors.Is(err, audit.ErrCorrectionTampered) {
		t.Fatalf("legality got %v", err)
	}
	if _, err := svc.ListCorrections(rec.ID); !errors.Is(err, audit.ErrCorrectionTampered) {
		t.Fatalf("list got %v", err)
	}
}

func TestConcurrentLinearizability(t *testing.T) {
	svc, _ := newSvc(t)
	commitV(t, svc, "v1", "",
		allowRule("allow-read", 1, nil, nil, []string{"read"}),
		denyRule("deny-write", 1, nil, nil, []string{"write"}),
	)

	const goroutines = 16
	const perG = 60
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				action := "read"
				if (g+i)%3 == 0 {
					action = "write"
				}
				rec, err := svc.RecordAudit(audit.AuditInput{
					Subject: fmt.Sprintf("u%d", g%4), Target: "doc", Action: action, RuleVersionID: "v1",
				})
				if err != nil {
					t.Errorf("record: %v", err)
					return
				}
				if _, err := svc.Replay(rec.ID); err != nil {
					t.Errorf("replay: %v", err)
					return
				}
				if _, err := svc.Legality(rec.ID); err != nil {
					t.Errorf("legality: %v", err)
				}
			}
		}(g)
	}
	// Concurrent version committers: each version must chain exactly once.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				head := ""
				if vs := svc.ListVersions(); len(vs) > 0 {
					head = vs[len(vs)-1].ID
				}
				_, _ = svc.CommitVersion(audit.RuleVersionInput{
					ID: fmt.Sprintf("vc-%d-%d", g, i), ParentID: head,
					Rules: audit.RuleSet{Rules: []audit.Rule{denyRule("d", 1, nil, nil, nil)}},
				})
			}
		}(g)
	}
	wg.Wait()

	audits := svc.ListAudits()
	if len(audits) != goroutines*perG {
		t.Fatalf("audits=%d want %d", len(audits), goroutines*perG)
	}
	seqs := map[int64]bool{}
	var prevHash string
	for _, a := range audits {
		if seqs[a.Seq] {
			t.Fatalf("dup seq %d", a.Seq)
		}
		seqs[a.Seq] = true
		if a.PrevHash != prevHash {
			t.Fatalf("audit chain broken at seq %d", a.Seq)
		}
		prevHash = a.RecordHash
	}
	if problems := svc.VerifyAll(); len(problems) != 0 {
		t.Fatalf("integrity problems after concurrency: %v", problems)
	}
}
