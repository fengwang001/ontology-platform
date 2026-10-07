package audit_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ontology/audit"
)

// TestCallLogCompleteness verifies every call logs inputs, final output (or the
// classified error) and the version/record basis used for the decision.
func TestCallLogCompleteness(t *testing.T) {
	svc, logger := newSvc(t)
	commitV(t, svc, "v1", "", denyRule("r1", 1, nil, nil, nil))
	rec, _ := svc.RecordAuditWithEngine(
		audit.AuditInput{Subject: "alice", Target: "d", Action: "read", Content: "body", RuleVersionID: "v1"},
		audit.StaticEngine{Name: "buggy", Answer: true},
	)
	if _, err := svc.Replay(rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{
		ID: "c1", AuditID: rec.ID, CorrectedAllow: false, Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Legality(rec.ID); err != nil {
		t.Fatal(err)
	}
	// Failed calls must be logged too, with the stable error code.
	if _, err := svc.Replay("missing"); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := svc.AppendCorrection(audit.AppendCorrectionInput{ID: "x", AuditID: rec.ID, CorrectedAllow: false}); err == nil {
		t.Fatal("expected duplicate conflict")
	}

	entries := logger.Entries()
	if len(entries) == 0 {
		t.Fatal("no log entries")
	}
	methods := map[string]bool{}
	for _, e := range entries {
		if e.Seq == 0 || e.Method == "" || e.Input == nil {
			t.Fatalf("entry missing seq/method/input: %+v", e)
		}
		b, err := e.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(b) {
			t.Fatalf("invalid json: %s", b)
		}
		methods[e.Method] = true
	}
	for _, m := range []string{"CommitVersion", "RecordAudit", "Replay", "AppendCorrection", "Legality"} {
		if !methods[m] {
			t.Fatalf("method %s was not logged", m)
		}
	}

	// Find the replay entry and assert the basis names version and audit.
	var replayLogged, corrLogged, failLogged bool
	for _, e := range entries {
		switch {
		case e.Method == "Replay" && e.Basis != nil && e.Basis.RuleVersionID == "v1" && e.Basis.AuditID == rec.ID:
			replayLogged = true
			if e.Output == nil {
				t.Fatal("successful replay missing output")
			}
		case e.Method == "AppendCorrection" && e.Basis != nil && e.Basis.CorrectionID == "c1":
			corrLogged = true
		case e.Method == "Replay" && e.ErrorCode != "":
			failLogged = true
			if e.ErrorCode != (audit.ErrAuditNotFound).Error() {
				t.Fatalf("error code = %q", e.ErrorCode)
			}
		}
	}
	if !replayLogged || !corrLogged || !failLogged {
		t.Fatalf("log coverage replay=%v corr=%v fail=%v", replayLogged, corrLogged, failLogged)
	}

	// JSON Lines rendering works and carries the decision basis verbatim.
	var sb strings.Builder
	if err := logger.WriteJSONLines(newLineWriter(&sb)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `"rule_version_id":"v1"`) {
		t.Fatalf("jsonl missing basis: %s", sb.String())
	}
}

type lineWriter struct{ sb *strings.Builder }

func newLineWriter(sb *strings.Builder) *lineWriter { return &lineWriter{sb: sb} }

func (w *lineWriter) Write(p []byte) (int, error) { return w.sb.Write(p) }
