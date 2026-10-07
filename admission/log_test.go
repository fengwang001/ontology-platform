package admission

import (
	"fmt"
	"strings"
	"testing"
)

// opLogger 记录每次操作的输入、实际输出与判定依据，测试时随日志输出。
type opLogger struct {
	t    *testing.T
	sb   strings.Builder
	step int
}

func newOpLogger(t *testing.T) *opLogger {
	t.Helper()
	return &opLogger{t: t}
}

func (l *opLogger) logSubmit(req *Request, now Time, res *OpResult, basis string) {
	l.step++
	dec := "-"
	level := "-"
	flow := "-"
	err := "-"
	if res.Submit != nil {
		dec = decisionName(res.Submit.Decision)
		level = dash(res.Submit.Level)
		flow = dash(res.Submit.Flow)
		if res.Submit.Err != nil {
			err = res.Submit.Err.Error()
		}
	}
	fmt.Fprintf(&l.sb, "[%03d] t=%d SUBMIT id=%s group=%s verb=%s resource=%s user=%s ns=%s seats=%d => %s level=%s flow=%s err=%s | events=%s | 依据: %s\n",
		l.step, now, req.ID, req.UserGroup, req.Verb, req.Resource, req.User, req.Namespace, req.Seats,
		dec, level, flow, err, formatEvents(res.Events), basis)
}

func (l *opLogger) logComplete(id string, now Time, res *OpResult, basis string) {
	l.step++
	fmt.Fprintf(&l.sb, "[%03d] t=%d COMPLETE id=%s => events=%s | 依据: %s\n",
		l.step, now, id, formatEvents(res.Events), basis)
}

func (l *opLogger) logUpdate(now Time, res *OpResult, err error, basis string) {
	l.step++
	errText := "-"
	if err != nil {
		errText = err.Error()
	}
	fmt.Fprintf(&l.sb, "[%03d] t=%d UPDATE => events=%s err=%s | 依据: %s\n",
		l.step, now, formatEvents(ev(res)), errText, basis)
}

func (l *opLogger) logNote(format string, args ...any) {
	l.step++
	fmt.Fprintf(&l.sb, "[%03d] NOTE: %s\n", l.step, fmt.Sprintf(format, args...))
}

func (l *opLogger) dump() string { return l.sb.String() }

func ev(res *OpResult) []Event {
	if res == nil {
		return nil
	}
	return res.Events
}

func decisionName(d Decision) string {
	switch d {
	case DecisionExecuted:
		return "EXECUTED"
	case DecisionQueued:
		return "QUEUED"
	default:
		return "REJECTED"
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatEvents(events []Event) string {
	if len(events) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(events))
	for _, e := range events {
		parts = append(parts, fmt.Sprintf("%s(%s,lvl=%s,flow=%s,seats=%d,deadline=%d)",
			e.Kind, e.ID, e.Level, e.Flow, e.Seats, e.Deadline))
	}
	return strings.Join(parts, ";")
}

func eventIDs(events []Event, kind string) []string {
	var out []string
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e.ID)
		}
	}
	return out
}
