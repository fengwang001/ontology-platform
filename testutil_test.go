package ontology

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// testLogger 在测试日志中打印每次输入、输出与判定依据。
// 失败时额外写入到 testing.T；所有内容也始终写到 stdout，满足“日志中打印”。
type testLogger struct {
	t   *testing.T
	tag string
	sb  strings.Builder
}

func newLogger(t *testing.T, tag string) *testLogger {
	l := &testLogger{t: t, tag: tag}
	l.logf("==== 用例 %s 开始 ====", tag)
	return l
}

func (l *testLogger) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.sb.WriteString(line)
	l.sb.WriteByte('\n')
	fmt.Fprintln(os.Stdout, line)
}

func (l *testLogger) input(format string, args ...any) {
	l.logf("[输入] "+format, args...)
}

func (l *testLogger) output(format string, args ...any) {
	l.logf("[输出] "+format, args...)
}

func (l *testLogger) reason(format string, args ...any) {
	l.logf("[判定依据] "+format, args...)
}

func (l *testLogger) finish() {
	l.logf("==== 用例 %s 结束 ====", l.tag)
}

func (l *testLogger) fatalf(format string, args ...any) {
	l.logf("[失败] "+format, args...)
	l.t.Fatalf("%s", l.sb.String())
}

// renderType 渲染类型为确定性的紧凑字符串，便于断言与日志对照。
func renderType(t *Type) string {
	if t == nil {
		return "<nil>"
	}
	n, err := normalize(t)
	if err != nil {
		return "<malformed:" + err.Error() + ">"
	}
	return normKey(n)
}

func mustAnalyze(t *testing.T, p *Program, tag string) *Result {
	t.Helper()
	r, e := Analyze(t.Context(), p)
	if e != nil {
		t.Fatalf("%s: 预期分析成功，得到错误 %v", tag, e)
	}
	return r
}

func expectTypeAt(t *testing.T, r *Result, id, variable, want string, l *testLogger) {
	t.Helper()
	got, err := r.Query(id, variable)
	if err != nil {
		l.fatalf("查询 %q/%s 出错: %v", id, variable, err)
	}
	if got.Status != PointReachable {
		l.fatalf("%q/%s 预期可达，实际不可达", id, variable)
	}
	gs := renderType(got.Type)
	l.output("点 %q 变量 %s 窄化为 %s", id, variable, gs)
	if gs != want {
		l.fatalf("%q/%s 期望 %s，实际 %s", id, variable, want, gs)
	}
}

func expectUnreachable(t *testing.T, r *Result, id string, l *testLogger) {
	t.Helper()
	st, err := r.PointReachable(id)
	if err != nil {
		l.fatalf("查询点 %q 出错: %v", id, err)
	}
	l.output("点 %q 可达性=%d（0=可达,1=不可达）", id, st)
	if st != PointUnreachable {
		l.fatalf("点 %q 预期不可达，实际可达", id)
	}
}
