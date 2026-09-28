package txnset

import (
	"log/slog"
	"sync/atomic"
)

// activeLogger 为 nil 时关闭日志；*slog.Logger 自身可并发使用。
var activeLogger atomic.Pointer[slog.Logger]

// SetLogger 设置解析、合并与求差过程的结构化日志输出；传 nil 关闭日志。
// 每次调用的日志包含输入文本、规范文本与判定依据（basis）。
func SetLogger(l *slog.Logger) {
	activeLogger.Store(l)
}

func logParse(text, canonical string, err error) {
	l := activeLogger.Load()
	if l == nil {
		return
	}
	args := []any{"input", text}
	if err != nil {
		l.Info("txnset parse rejected", append(args,
			"basis", "left-to-right scan, first error wins",
			"error", err.Error())...)
		return
	}
	l.Info("txnset parse accepted", append(args,
		"canonical", canonical,
		"basis", "group by source, sort intervals, merge overlap and adjacency")...)
}

func logMerge(text, canonical, basis string, err error) {
	l := activeLogger.Load()
	if l == nil {
		return
	}
	if err != nil {
		l.Info("txnset merge rejected",
			"input", text,
			"basis", "parse fully before applying, state unchanged on error",
			"error", err.Error())
		return
	}
	l.Info("txnset merge applied",
		"input", text, "canonical", canonical, "basis", basis)
}

func logDiff(a, b, result string) {
	l := activeLogger.Load()
	if l == nil {
		return
	}
	l.Info("txnset diff",
		"minuend", a, "subtrahend", b, "canonical", result,
		"basis", "per-source closed-interval subtraction, inputs unchanged")
}
