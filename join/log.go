package join

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// defaultLogger 返回写 stderr 的文本格式 logger，作为未显式注入时的默认值。
func defaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// applyLogger 把每次 Apply 的输入、输出差分与判定依据结构化地写入 slog。
// 所有方法均为 nil 安全：未配置日志时静默跳过。
type applyLogger struct {
	log *slog.Logger
}

func newApplyLogger(l *slog.Logger) *applyLogger {
	if l == nil {
		l = defaultLogger()
	}
	return &applyLogger{log: l}
}

// logStart 记录批次输入（两侧变更）。
func (l *applyLogger) logStart(seq int64, b Batch) {
	if l == nil || l.log == nil {
		return
	}
	l.log.Info("join.apply.start",
		slog.Int64("seq", seq),
		slog.String("left", formatChanges(b.Left)),
		slog.String("right", formatChanges(b.Right)),
	)
}

// logAccepted 记录通过判定及输出差分；resultSize 为提交后物化结果中的不同元组数。
func (l *applyLogger) logAccepted(seq int64, b Batch, deltas []JoinDelta, resultSize int) {
	if l == nil || l.log == nil {
		return
	}
	l.log.Info("join.apply.accepted",
		slog.Int64("seq", seq),
		slog.String("decision", ReasonOK.String()),
		slog.String("left", formatChanges(b.Left)),
		slog.String("right", formatChanges(b.Right)),
		slog.String("deltas", formatDeltas(deltas)),
		slog.Int("delta_count", len(deltas)),
		slog.Int("result_tuple_count", resultSize),
	)
}

// logRejected 记录拒绝判定及其可区分原因；拒绝批次不落任何状态。
func (l *applyLogger) logRejected(seq int64, b Batch, err *RejectError) {
	if l == nil || l.log == nil {
		return
	}
	args := []any{
		slog.Int64("seq", seq),
		slog.String("decision", "rejected"),
		slog.String("reason", err.Reason.String()),
		slog.String("left", formatChanges(b.Left)),
		slog.String("right", formatChanges(b.Right)),
		slog.String("deltas", "[]"),
	}
	if err.Side != "" {
		args = append(args, slog.String("side", err.Side))
	}
	if err.Key != "" {
		args = append(args, slog.String("key", err.Key))
	}
	if err.Value != "" {
		args = append(args, slog.String("value", err.Value))
	}
	if err.Detail != "" {
		args = append(args, slog.String("detail", err.Detail))
	}
	l.log.Warn("join.apply.rejected", args...)
}

// formatDeltas 把差分列表格式化为稳定单行字符串，空列表为 "[]"。
func formatDeltas(deltas []JoinDelta) string {
	if len(deltas) == 0 {
		return "[]"
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, d := range deltas {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(key=")
		sb.WriteString(strconv.Quote(d.Key))
		sb.WriteString(", left=")
		sb.WriteString(strconv.Quote(d.LeftValue))
		sb.WriteString(", right=")
		sb.WriteString(strconv.Quote(d.RightValue))
		sb.WriteString(", delta=")
		sb.WriteString(strconv.FormatInt(d.Delta, 10))
		sb.WriteByte(')')
	}
	sb.WriteByte(']')
	return sb.String()
}
