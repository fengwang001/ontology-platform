package main

import (
	"bytes"
	"strings"
	"testing"

	"ontology/transaction"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		line string
		ev   transaction.Event
		ok   bool
	}{
		{"   ", transaction.Event{}, false},
		{"# a comment", transaction.Event{}, false},
		{"BEGIN a", transaction.Event{Type: transaction.EventBegin, TxID: "a"}, true},
		{"commit a", transaction.Event{Type: transaction.EventCommit, TxID: "a"}, true},
		{"ROLLBACK a", transaction.Event{Type: transaction.EventRollback, TxID: "a"}, true},
		{"WRITE a k v", transaction.Event{Type: transaction.EventWrite, TxID: "a", Row: transaction.Row{Key: "k", Value: "v"}}, true},
	}
	for _, c := range cases {
		ev, ok, err := parseLine(c.line)
		if err != nil {
			t.Fatalf("parseLine(%q) error: %v", c.line, err)
		}
		if ok != c.ok {
			t.Fatalf("parseLine(%q) ok = %v, want %v", c.line, ok, c.ok)
		}
		if ok && ev != c.ev {
			t.Fatalf("parseLine(%q) = %+v, want %+v", c.line, ev, c.ev)
		}
	}

	// 非法行必须报错而非被静默吞掉。
	for _, bad := range []string{"FROBNICATE a", "BEGIN", "WRITE a only-key", "COMMIT a b"} {
		if _, _, err := parseLine(bad); err == nil {
			t.Fatalf("parseLine(%q) expected error", bad)
		}
	}
}

// TestRunEndToEnd 用一段脚本驱动 run，断言提交按序送达、回滚行丢弃、
// 非法操作被拒绝但流程继续，且日志包含输入/输出/判定依据。
func TestRunEndToEnd(t *testing.T) {
	script := strings.NewReader(`
BEGIN a
BEGIN b
WRITE a 1 one
WRITE b 9 nine
ROLLBACK b
COMMIT a
COMMIT ghost
`)
	var out bytes.Buffer
	if err := run(script, &out, 8); err != nil {
		t.Fatal(err)
	}
	log := out.String()
	for _, want := range []string{
		"in  BEGIN tx=\"a\"",
		"ok  ROLLBACK tx=\"b\" dropped=1",
		"out COMMIT tx=\"a\" seq=1 rows=1 [\"1\"=\"one\"]",
		"rej COMMIT tx=\"ghost\" reason=" + string(transaction.ReasonTxNotFound),
		">>> delivered committed tx=a seq=1 rows=1",
		"summary: inflight=0 buffered=0",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("output missing %q, got:\n%s", want, log)
		}
	}
	// 回滚行 "9"="nine" 允许出现在输入日志，但绝不能出现在任何提交输出行。
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "out COMMIT") && strings.Contains(line, `"9"="nine"`) {
			t.Fatalf("rolled-back row leaked into commit output: %s", line)
		}
	}
}
