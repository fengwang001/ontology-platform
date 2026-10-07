package ontologyindex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestJSONLAuditorPersists：判定记录必须落盘为 JSONL，事后可逐条核查
// 输入事件、所依据索引版本与判定结论。
func TestJSONLAuditorPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	aud, err := NewJSONLAuditor(path)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(schemaForTests(), aud)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	if err := eng.Ingest(ChangeEvent{EventID: "x1", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("A"), EffectiveAt: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Lookup("idx", StringValue("A")); err != nil {
		t.Fatal(err)
	}
	if err := aud.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []AuditRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r AuditRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("审计行不是合法 JSON: %v", err)
		}
		lines = append(lines, r)
	}
	if len(lines) != 3 {
		t.Fatalf("期望 3 条审计记录(create/ingest/lookup)，得到 %d", len(lines))
	}
	if lines[1].EventID != "x1" || lines[1].IndexVersion != 1 || lines[1].Decision != "accepted" {
		t.Fatalf("ingest 审计内容不符: %+v", lines[1])
	}
	if lines[2].Op != "lookup" || lines[2].Decision != "returned" {
		t.Fatalf("lookup 审计内容不符: %+v", lines[2])
	}
}
