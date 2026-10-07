package ontology_test

import (
	"fmt"
	"testing"

	"ontology/ontology"
)

// testLogger 按测试要求打印"输入、实际输出与据以判定的依据"。
// 每条用例都通过 t.Logf 落一条可追溯记录。
type testLogger struct {
	t   *testing.T
	seq int
}

func newLogger(t *testing.T) *testLogger {
	t.Helper()
	return &testLogger{t: t}
}

func (l *testLogger) step(input string, got, basis any) {
	l.seq++
	l.t.Logf("[#%03d] 输入=%s | 实际输出=%v | 判定依据=%v", l.seq, input, fmtStr(got), basis)
}

func fmtStr(v any) string { return fmt.Sprintf("%+v", v) }

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 期望成功, 实际错误=%v", ctx, err)
	}
}

func baseMappings() []ontology.Mapping {
	return []ontology.Mapping{
		{Attr: "name", Kind: ontology.KindKeep},
		{Attr: "age", Kind: ontology.KindKeep},
		{Attr: "deprecated", Kind: ontology.KindDrop},
		{Attr: "level", Kind: ontology.KindAdd, Default: ontology.Present("L0")},
	}
}

func startBaseStore(t *testing.T, preCreate ...string) *ontology.Store {
	t.Helper()
	s := ontology.NewStore("Person", ontology.VersionOld, ontology.VersionNew)
	for _, id := range preCreate {
		if err := s.Create(id, ontology.VersionOld, ontology.Props{
			"name":       ontology.Present(id + "-n"),
			"age":        ontology.Present(30),
			"deprecated": ontology.Present("old-only"),
		}); err != nil {
			t.Fatalf("预置实例 %s: %v", id, err)
		}
	}
	if err := s.StartMigration(ontology.Migration{
		ObjectType: "Person", From: ontology.VersionOld, To: ontology.VersionNew,
		Mappings: baseMappings(),
	}); err != nil {
		t.Fatalf("发起迁移: %v", err)
	}
	return s
}
