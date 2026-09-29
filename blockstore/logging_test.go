package blockstore

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

type bufLogger struct{ buf bytes.Buffer }

func (l *bufLogger) Printf(format string, args ...any) {
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

// 日志中必须打印输入、输出与判定依据。
func TestLogsContainInputOutputDecision(t *testing.T) {
	logger := &bufLogger{}
	st := NewWithLogger(0, logger)
	st.BeginSession("s1")
	_ = st.Upload("s1", dstr(1), []byte("ab"))
	_ = st.Commit("s1", "m1", []Digest{dstr(1)})
	_, _ = st.GCRound1()
	_ = st.Commit("s1", "m2", []Digest{dstr(1)}) // 重复提交
	_ = st.EndSession("s1")
	_, _ = st.GCRound2()

	log := logger.buf.String()
	for op, frag := range map[string][]string{
		"Upload":     {"in={", "out=stored", "decision=content-new"},
		"Commit":     {"in={", "out=committed", "decision=all-blocks-present"},
		"GCRound1":   {"out={", "decision=mark-unreferenced"},
		"GCRound2":   {"out={", "decision=witnesses-closed"},
		"EndSession": {"in={", "out=ok", "decision=ended"},
	} {
		var opLines []string
		for _, line := range strings.Split(log, "\n") {
			if strings.Contains(line, "op="+op) {
				opLines = append(opLines, line)
			}
		}
		if len(opLines) == 0 {
			t.Fatalf("no log line for op=%s\n%s", op, log)
		}
		for _, f := range frag {
			found := false
			for _, opLine := range opLines {
				if strings.Contains(opLine, f) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("op=%s lines %v missing %q", op, opLines, f)
			}
		}
	}
	if !strings.Contains(log, "decision=duplicate-commit") {
		t.Fatalf("rejection decision not logged:\n%s", log)
	}
}
