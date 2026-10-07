package audit_test

import (
	"fmt"

	"ontology/audit"
)

// Example shows the full lifecycle: commit a rule version, record a decision
// made by a defective engine, replay it, append a correction and query the
// three-way legality view.
func Example() {
	logger := audit.NewSliceLogger()
	svc := audit.NewService(logger)

	denyAll := audit.Rule{ID: "deny", Order: 1, Effect: audit.EffectDeny}
	v, err := svc.CommitVersion(audit.RuleVersionInput{ID: "v1", Rules: audit.RuleSet{Rules: []audit.Rule{denyAll}}})
	if err != nil {
		panic(err)
	}

	// A defective historical engine wrongly allowed the request.
	rec, err := svc.RecordAuditWithEngine(
		audit.AuditInput{Subject: "alice", Target: "doc1", Action: "read", RuleVersionID: v.ID},
		audit.StaticEngine{Name: "defective-2026-09", Answer: true},
	)
	if err != nil {
		panic(err)
	}

	// Replay rebuilds the decision from v1 alone and finds the original flawed.
	report, err := svc.Replay(rec.ID)
	if err != nil {
		panic(err)
	}
	fmt.Println(report.Outcome)

	// Append an independent correction; the original record stays untouched.
	corr, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{
		ID: "corr-1", AuditID: rec.ID, CorrectedAllow: false, Reason: "engine defect",
	})
	if err != nil {
		panic(err)
	}

	view, err := svc.Legality(rec.ID)
	if err != nil {
		panic(err)
	}
	fmt.Println(view.Kind, view.EffectiveAllow, corr.AuditID)

	// Output:
	// MISMATCH_DEFECTIVE_ORIGINAL
	// CORRECTED false audit-1
}
