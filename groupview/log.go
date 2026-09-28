package groupview

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func opName(op Op) string {
	switch op {
	case Insert:
		return "INSERT"
	case Delete:
		return "DELETE"
	default:
		return fmt.Sprintf("OP(%d)", op)
	}
}

func kindName(k EntryKind) string {
	switch k {
	case Retract:
		return "RETRACT"
	case Upsert:
		return "UPSERT"
	default:
		return fmt.Sprintf("KIND(%d)", k)
	}
}

// decision records why a touched group produced (or skipped) entries, so the
// log can show the exact in-view/out-view comparison and its basis.
type decision struct {
	group   string
	before  Aggregate
	after   Aggregate
	inView  bool
	outView bool
	action  string
}

func (d decision) explain(cfg Config) string {
	return fmt.Sprintf(
		"group=%q before=(count=%d,sum=%d,inView=%t) "+
			"after=(count=%d,sum=%d,inView=%t) decision=%s rule=(count>=%d && sum>=%d)",
		d.group,
		d.before.Count, d.before.Sum, d.inView,
		d.after.Count, d.after.Sum, d.outView,
		d.action, cfg.MinCount, cfg.MinSum,
	)
}

// logger writes deterministic, human-readable lines for every applied or
// rejected batch. Output order depends only on the input sequence.
type logger struct {
	w io.Writer
}

func (l *logger) logf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	fmt.Fprintf(l.w, format+"\n", args...)
}

// bufferedLogger stages a batch's full audit trail. On commit the staged
// lines are flushed; on rejection they are discarded, so an invalid batch
// contributes nothing to the produced change log.
type bufferedLogger struct {
	w  io.Writer
	sb strings.Builder
}

func (l *bufferedLogger) logf(format string, args ...any) {
	if l == nil {
		return
	}
	fmt.Fprintf(&l.sb, format+"\n", args...)
}

func (l *bufferedLogger) flush() {
	if l == nil || l.w == nil {
		return
	}
	io.WriteString(l.w, l.sb.String())
}

func (l *bufferedLogger) reset() {
	if l == nil {
		return
	}
	l.sb.Reset()
}

// rejectionLogger writes a rejection line directly, bypassing the stage.
func (l *bufferedLogger) rejectf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	fmt.Fprintf(l.w, format+"\n", args...)
}

func mutationText(m Mutation) string {
	return fmt.Sprintf("%s row=%q group=%q value=%d",
		opName(m.Op), m.RowID, m.Group, m.Value)
}

func entryText(e Entry) string {
	return fmt.Sprintf("%s group=%q count=%d sum=%d",
		kindName(e.Kind), e.Group, e.Value.Count, e.Value.Sum)
}
