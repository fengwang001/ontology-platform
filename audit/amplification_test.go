package audit_test

import (
	"strings"
	"testing"

	"ontology/audit"
)

// TestReplayReadsOnlyNamedVersion is the implementation-independent,
// observable proof that replay read volume is bounded by the rule content of
// the single version named in the record, and does not grow with the total
// number of versions.
//
// Observable counters (VersionsTouched / VersionBytesRead) are produced by the
// storage layer at the point-lookup boundary, not by the replay algorithm, so
// they do not depend on replay internals.
func TestReplayReadsOnlyNamedVersion(t *testing.T) {
	svc, _ := newSvc(t)

	// A small fixed-size rule set used by the version under test.
	smallRules := audit.RuleSet{Rules: []audit.Rule{
		allowRule("r-allow", 1, []string{"alice"}, []string{"doc1"}, []string{"read"}),
		denyRule("r-deny", 2, nil, nil, []string{"delete"}),
	}}

	commitV(t, svc, "v1", "", denyRule("r0", 1, nil, nil, nil))
	commitV(t, svc, "v2", "v1", smallRules.Rules...)

	// A probe record bound to v2 before we accumulate many later versions.
	rec, err := svc.RecordAudit(audit.AuditInput{
		Subject: "alice", Target: "doc1", Action: "read", RuleVersionID: "v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := svc.Replay(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.VersionsTouched != 1 {
		t.Fatalf("baseline touched %d versions, want exactly 1", baseline.VersionsTouched)
	}

	// Large later versions: many rules each, far bigger than v2.
	big := make([]audit.Rule, 40)
	for i := range big {
		subjects := []string{"padding-user-with-a-very-long-name-0000000000000"}
		big[i] = audit.Rule{
			ID:       strings.Repeat("rule-", 2) + itoaLite(i),
			Order:    i,
			Subjects: subjects,
			Targets:  []string{strings.Repeat("target-", 8) + itoaLite(i)},
			Actions:  []string{"read", "write", "delete", "admin"},
			Effect:   audit.EffectDeny,
		}
	}
	parent := "v2"
	const laterCount = 60
	for i := 0; i < laterCount; i++ {
		id := "big-v-" + itoaLite(i)
		commitV(t, svc, id, parent, big...)
		parent = id
	}

	// Replaying the old record against the old version must show the same
	// single-version, same-byte read despite 60 extra large versions.
	after, err := svc.Replay(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.VersionsTouched != 1 {
		t.Fatalf("after growth touched %d versions, want 1", after.VersionsTouched)
	}
	if after.VersionBytesRead != baseline.VersionBytesRead {
		t.Fatalf("bytes grew with version count: %d -> %d", baseline.VersionBytesRead, after.VersionBytesRead)
	}

	// And the measured bytes equal the independently measured size of v2 only.
	v2, _ := svc.GetVersion("v2")
	wantBytes := audit.MeasureVersion(v2)
	if after.VersionBytesRead != wantBytes {
		t.Fatalf("read %d bytes but v2 content is %d bytes", after.VersionBytesRead, wantBytes)
	}

	// The bytes must scale with the named version's own content size, not be a
	// constant. Commit another record bound to a big version.
	bigRec, err := svc.RecordAudit(audit.AuditInput{
		Subject: "alice", Target: "doc1", Action: "read", RuleVersionID: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	bigReport, err := svc.Replay(bigRec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bigReport.VersionBytesRead <= after.VersionBytesRead {
		t.Fatalf("big version read %d should exceed small %d", bigReport.VersionBytesRead, after.VersionBytesRead)
	}
	if bigReport.VersionsTouched != 1 {
		t.Fatalf("big replay touched %d versions", bigReport.VersionsTouched)
	}
}

func itoaLite(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
