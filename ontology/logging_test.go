package ontology

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDecisionLogRecordsInputOutputBasis(t *testing.T) {
	ctx := context.Background()
	p := New()
	mustOK(t, p.CreateType(ctx, "Animal", nil))
	mustOK(t, p.CreateType(ctx, "Dog", []string{"Animal"}))
	mustOK(t, p.CreateSubject(ctx, "alice"))
	denyVersion, err := p.PutRule(ctx, "Animal", "alice", "medical", EffectDeny)
	mustOK(t, err)

	// 内存日志器断言字段完整性。
	sl := newSliceLogger()
	p.WithLogger(sl)
	_, err = p.Export(ctx, ExportRequest{ExportID: "log-1", TypeID: "Dog", SubjectID: "alice", Attributes: []string{"name", "medical"}})
	mustOK(t, err)

	recs := sl.snapshot()
	if len(recs) != 2 {
		t.Fatalf("expected one log per attribute decision, got %d", len(recs))
	}
	byAttr := map[string]DecisionLogRecord{}
	for _, r := range recs {
		byAttr[r.Attribute] = r
		if r.ExportID != "log-1" || r.Subject != "alice" || r.TypeID != "Dog" || r.Seq == 0 {
			t.Fatalf("log missing input context: %+v", r)
		}
	}
	if byAttr["medical"].Decision != "EXCLUDE" || byAttr["medical"].HitVersionID != denyVersion ||
		byAttr["medical"].DeclaringType != "Animal" || byAttr["medical"].Effect != "DENY" {
		t.Fatalf("exclude log missing basis: %+v", byAttr["medical"])
	}
	if byAttr["name"].Decision != "INCLUDE" {
		t.Fatalf("include decision not logged: %+v", byAttr["name"])
	}

	// JSON 行日志器输出可检索的输入/输出/依据。
	var buf bytes.Buffer
	p.WithLogger(NewJSONLogger(&buf))
	_, err = p.Export(ctx, ExportRequest{ExportID: "log-2", TypeID: "Dog", SubjectID: "alice", Attributes: []string{"medical"}})
	mustOK(t, err)
	line := buf.String()
	for _, want := range []string{`"export_id":"log-2"`, `"decision":"EXCLUDE"`, `"hit_version_id":"` + denyVersion + `"`, `"declaring_type":"Animal"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("json log missing %s in %s", want, line)
		}
	}
}
