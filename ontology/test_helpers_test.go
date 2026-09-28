package ontology

import (
	"fmt"
	"strings"
	"testing"
)

// logOp 在测试日志中打印一次操作的输入与结果时间戳，满足“打印输入、时间戳”的要求。
func logOp(t *testing.T, op string, node int, msg string, ev Event, err error) {
	t.Helper()
	if err != nil {
		t.Logf("输入: %s(node=%d, msg=%q) -> 拒绝: %v", op, node, msg, err)
		return
	}
	t.Logf("输入: %s(node=%d, msg=%q) -> 事件 (node=%d,seq=%d) 时间戳=%d 向量=%v",
		op, node, msg, ev.Node, ev.Seq, ev.Clock, ev.Vector)
}

// logRelation 打印因果判定的输入事件、结论与判定依据。
func logRelation(t *testing.T, a, b EventRef, r Relation, va, vb []int) {
	t.Helper()
	t.Logf("判定: (%d,%d)%v 与 (%d,%d)%v => %s；依据: 逐分量比较向量时钟",
		a.Node, a.Seq, va, b.Node, b.Seq, vb, r)
}

// formatOrder 以 “c/node:seq(kind)” 形式渲染全序，便于日志核对。
func formatOrder(es []Event) string {
	var b strings.Builder
	for i, e := range es {
		if i > 0 {
			b.WriteString(" < ")
		}
		fmt.Fprintf(&b, "%d/n%d:%d(%s)", e.Clock, e.Node, e.Seq, e.Kind)
	}
	return b.String()
}

// must 执行一个返回 (Event,error) 的操作，出错即 panic 使当前用例失败。
// 签名与 Local/Send/Receive 的返回值精确匹配，故可直接写 must(s.Local(0))。
func must(ev Event, err error) Event {
	if err != nil {
		panic(err)
	}
	return ev
}

// wantErrKind 断言 err 是 *ClockError 且 Kind 符合预期。
func wantErrKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %s, got nil", kind)
	}
	ce, ok := err.(*ClockError)
	if !ok {
		t.Fatalf("expected *ClockError, got %T: %v", err, err)
	}
	if ce.Kind != kind {
		t.Fatalf("expected error kind %s, got %s (%v)", kind, ce.Kind, err)
	}
}
