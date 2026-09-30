package ontology

import (
	"strings"
	"testing"
)

// 日志需包含输入、输出与判定依据。
func TestLogsContainInputsOutputsAndBasis(t *testing.T) {
	p, logBuf := basicGraph(t)

	if err := p.ExternalWrite("S", 4); err != nil {
		t.Fatal(err)
	}
	run := mustStart(t, p, "A", 4)
	if err := p.Complete(run, true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Plan([]PartitionRef{{Asset: "A", Partition: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Impact("S", 4); err != nil {
		t.Fatal(err)
	}

	logText := logBuf.String()
	for _, want := range []string{
		"start OK asset=A part=4",         // 输入操作
		"inputs=S#4@1",                    // 判定输入（消费快照）
		"complete OK run=1",               // 完成输出
		"staleAtBirth=false",              // 判定依据
		"plan OK",                         // 规划输出
		"impact OK root=S#4 result=[A#4]", // 影响面输出
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}

	// 过期路径日志要给出具体依据（版本不一致）。
	if err := p.ExternalWrite("S", 4); err != nil {
		t.Fatal(err)
	}
	logBuf.Reset()
	if _, err := p.Plan([]PartitionRef{{Asset: "A", Partition: 4}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logBuf.String(), "version 2 != consumed 1") {
		t.Fatalf("stale basis not logged:\n%s", logBuf.String())
	}
}
