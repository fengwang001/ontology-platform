package compensation

import (
	"fmt"
	"io"
	"os"
)

// LogTracer 把每次动作与补偿的输入、每一步生效/撤销结果与最终判定依据
// 逐行打印到 Writer（默认 os.Stdout）。
type LogTracer struct {
	W      io.Writer
	Indent string
}

// NewLogTracer 创建输出到 stdout 的日志 tracer。
func NewLogTracer() *LogTracer {
	return &LogTracer{W: os.Stdout}
}

func (t *LogTracer) w() io.Writer {
	if t.W == nil {
		return io.Discard
	}
	return t.W
}

// ActionStart 打印动作输入摘要。
func (t *LogTracer) ActionStart(actionID string, opCount int) {
	fmt.Fprintf(t.w(), "%s[action %s] start, %d ordered sub-op(s):\n", t.Indent, actionID, opCount)
}

// StepApplied 打印子操作生效与其逆操作登记编号。
func (t *LogTracer) StepApplied(actionID string, step int, entry uint64, detail string) {
	fmt.Fprintf(t.w(), "%s  [action %s] step %d APPLIED (inverse entry #%d): %s\n",
		t.Indent, actionID, step, entry, detail)
}

// StepRejected 打印子操作被拒绝及其类别。
func (t *LogTracer) StepRejected(actionID string, step int, category Category, reason string) {
	fmt.Fprintf(t.w(), "%s  [action %s] step %d REJECTED(%s): %s\n",
		t.Indent, actionID, step, category, reason)
}

// CompStart 打印补偿开始。
func (t *LogTracer) CompStart(actionID string, count int) {
	fmt.Fprintf(t.w(), "%s  [action %s] compensation start for %d applied step(s), reverse order\n",
		t.Indent, actionID, count)
}

// CompStep 打印单个逆操作环节结果。
func (t *LogTracer) CompStep(actionID string, rec CompRecord) {
	switch {
	case rec.Skipped:
		fmt.Fprintf(t.w(), "%s    [action %s] inverse of step %d (entry #%d) SKIPPED: %s\n",
			t.Indent, actionID, rec.Index, rec.Entry, rec.Reason)
	case rec.OK:
		fmt.Fprintf(t.w(), "%s    [action %s] inverse of step %d (entry #%d) OK\n",
			t.Indent, actionID, rec.Index, rec.Entry)
	case rec.Panicked:
		fmt.Fprintf(t.w(), "%s    [action %s] inverse of step %d (entry #%d) FAILED[exception]: %s\n",
			t.Indent, actionID, rec.Index, rec.Entry, rec.Reason)
	default:
		fmt.Fprintf(t.w(), "%s    [action %s] inverse of step %d (entry #%d) FAILED: %s\n",
			t.Indent, actionID, rec.Index, rec.Entry, rec.Reason)
	}
}

// CompDone 打印补偿结束。
func (t *LogTracer) CompDone(actionID string) {
	fmt.Fprintf(t.w(), "%s  [action %s] compensation done\n", t.Indent, actionID)
}

// ActionDone 打印最终判定依据。
func (t *LogTracer) ActionDone(actionID string, category Category, reason string) {
	fmt.Fprintf(t.w(), "%s[action %s] FINAL %s :: %s\n\n", t.Indent, actionID, category, reason)
}
