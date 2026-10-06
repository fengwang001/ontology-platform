package collab

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// TextLogger prints every Sync call: inputs, per-operation verdicts with the
// current value/version that drove the decision, and the rejection reason.
// It is safe for concurrent use.
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger writes audit lines to stderr by default.
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stderr
	}
	return &TextLogger{w: w}
}

// LogBatch implements Logger.
func (l *TextLogger) LogBatch(client, docID string, ops []Op, now int64, results []OpResult, rejected error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "SYNC client=%s doc=%s now=%d ops=%d\n", client, docID, now, len(ops))
	for i, op := range ops {
		dep := ""
		if op.Depends {
			dep = " depends"
		}
		if op.Type == OpSet {
			fmt.Fprintf(l.w, "  IN  seq=%d SET %s=%d baseVer=%d group=%s%s\n",
				op.Seq, op.Field, op.Value, op.BaseVer, op.Group, dep)
		} else {
			fmt.Fprintf(l.w, "  IN  seq=%d ADD %s%+d group=%s%s\n",
				op.Seq, op.Field, op.Delta, op.Group, dep)
		}
		if i < len(results) {
			r := results[i]
			switch r.Kind {
			case ResConflict, ResAhead, ResOverflow, ResKindMismatch:
				fmt.Fprintf(l.w, "  OUT seq=%d %s currentValue=%d currentVersion=%d present=%t\n",
					r.Seq, r.Kind, r.Value, r.Version, r.Present)
			default:
				fmt.Fprintf(l.w, "  OUT seq=%d %s\n", r.Seq, r.Kind)
			}
		}
	}
	if rejected != nil {
		fmt.Fprintf(l.w, "  REJECTED %s\n", rejected)
	}
}
