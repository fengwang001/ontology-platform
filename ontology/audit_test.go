package ontology

import "testing"

// TestAuditLogRecordsEveryCall verifies that mutations and decisions are
// logged with their input, final output and — for decisions — the tag and
// role basis the outcome rests on.
func TestAuditLogRecordsEveryCall(t *testing.T) {
	logger := NewMemoryLogger(64)
	e := NewEngine(WithAuditLogger(logger))
	must(t, e.DeclareObjectType("A"))
	must(t, e.DeclareTag("t"))
	must(t, e.AttachTag("t", "A"))
	must(t, e.DeclareInstance("i", "A"))
	must(t, e.DeclareRole("r"))
	must(t, e.DeclareSubject("s", "r"))
	must(t, e.SetGrant("r", "t", Deny))

	dec, err := e.Authorize("s", "i", "read")
	if err != nil || dec.Allowed {
		t.Fatalf("expected clean denial, got %+v err=%v", dec, err)
	}

	events := logger.Events()
	if len(events) != 8 {
		t.Fatalf("expected 8 audit events, got %d", len(events))
	}
	last := events[len(events)-1]
	if last.Op != "Authorize" {
		t.Fatalf("last event op = %q", last.Op)
	}
	input, ok := last.Input.(map[string]string)
	if !ok || input["subject"] != "s" || input["instance"] != "i" || input["action"] != "read" {
		t.Fatalf("audit input incomplete: %+v", last.Input)
	}
	out, ok := last.Output.(Decision)
	if !ok {
		t.Fatalf("audit output missing decision: %+v", last.Output)
	}
	if out.Reason != ReasonExplicitDenyOverride {
		t.Fatalf("audit output reason = %q", out.Reason)
	}
	if len(out.Tags) != 1 || out.Tags[0].Tag != "t" {
		t.Fatalf("audit output missing tag basis: %+v", out.Tags)
	}
	basis := out.Tags[0].Basis
	if len(basis) != 1 || basis[0].Role != "r" || basis[0].Effect != Deny || !basis[0].Direct {
		t.Fatalf("audit output missing role basis: %+v", basis)
	}
}
