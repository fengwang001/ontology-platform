package compensate

import (
	"fmt"
	"io"
)

// TraceEvent describes one observable step of action/compensation.
type TraceEvent struct {
	ActionName string
	OpIndex    int
	Phase      string // "apply" | "undo" | "action"
	Kind       string
	Outcome    string // "effect" | "undo" | "reject" | "fail" | "panic" | "verdict"
	Detail     string
}

// Tracer receives every step result for logging and differential tests.
type Tracer interface {
	Event(ev TraceEvent)
}

// MultiTracer fans events out to several tracers.
type MultiTracer []Tracer

func (m MultiTracer) Event(ev TraceEvent) {
	for _, t := range m {
		if t != nil {
			t.Event(ev)
		}
	}
}

// SliceTracer records events in memory (assertions / differential oracle).
type SliceTracer struct{ Events []TraceEvent }

func (t *SliceTracer) Event(ev TraceEvent) { t.Events = append(t.Events, ev) }

// LogTracer writes human readable lines to a writer.
type LogTracer struct {
	W io.Writer
}

func (t *LogTracer) Event(ev TraceEvent) {
	if t == nil || t.W == nil {
		return
	}
	loc := ev.Phase
	if loc == "" {
		loc = "-"
	}
	fmt.Fprintf(t.W, "[%s] %-7s op#%-3d %-14s %s %s\n",
		ev.ActionName, loc, ev.OpIndex, ev.Kind, ev.Outcome, ev.Detail)
}
