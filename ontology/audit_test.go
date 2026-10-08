package ontology

import (
	"strings"
	"testing"
)

// TestAuditLogRecordsDecisions 每次判定（展开/迁移）的输入、所依据的
// 属性定义版本与结论都必须被记录，以便事后核查。
func TestAuditLogRecordsDecisions(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 0)
	mustRegister(t, s, "T", "o1")
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 1, RecordTime: 100,
		Values: map[string]Value{"a": IntValue(1)}})

	// 一次成功迁移、一次失败迁移、一次成功展开、一次失败展开。
	if err := s.Migrate(Migration{TypeID: "T", EffectiveFrom: 200, NewProps: simpleProps()}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	_ = s.Migrate(Migration{TypeID: "T", EffectiveFrom: 0, NewProps: map[string]PropertyDef{
		"a": {Name: "a", Type: TypeInt, Required: true},
		"r": {Name: "r", Type: TypeInt, Required: true},
	}})
	if _, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 300, ValidFrom: 0, ValidTo: 10}); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	_, _ = s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1, ValidFrom: 0, ValidTo: 10})

	log := s.AuditLog()
	if len(log) != 3 { // 成功迁移 + 失败迁移 + 成功展开（失败展开在记录前返回）
		t.Fatalf("got %d audit records, want 3", len(log))
	}

	// 编号单调连续。
	for i, r := range log {
		if r.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d", i)
		}
	}

	// 成功迁移：记录输入与新版本号。
	if log[0].Op != OpMigrate || log[0].Verdict != VerdictOK {
		t.Fatalf("record 0: %+v", log[0])
	}
	if !strings.Contains(log[0].Input, "effectiveFrom=200") {
		t.Fatalf("migrate input not recorded: %q", log[0].Input)
	}
	if len(log[0].Versions) != 1 || log[0].Versions[0] != 2 {
		t.Fatalf("migrate versions not recorded: %v", log[0].Versions)
	}

	// 失败迁移：记录错误结论与详情。
	if log[1].Op != OpMigrate || log[1].Verdict != VerdictError {
		t.Fatalf("record 1: %+v", log[1])
	}
	if !strings.Contains(log[1].Detail, ErrCodeMigrationValidation.String()) {
		t.Fatalf("failure detail not recorded: %q", log[1].Detail)
	}

	// 成功展开：记录输入与所依据的版本。
	if log[2].Op != OpExpand || log[2].Verdict != VerdictOK {
		t.Fatalf("record 2: %+v", log[2])
	}
	if !strings.Contains(log[2].Input, "asOfRecord=300") {
		t.Fatalf("expand input not recorded: %q", log[2].Input)
	}
	if len(log[2].Versions) != 1 || log[2].Versions[0] != 1 {
		t.Fatalf("expand versions not recorded: %v", log[2].Versions)
	}
}
