package cg

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Decision describes why an operation was accepted or rejected.
type Decision struct {
	At      int64
	Op      string
	Input   string
	Outcome string
	Reason  string
	Detail  string
}

// Tracer receives one Decision per operation.
type Tracer interface {
	Trace(d Decision)
}

// nopTracer drops decisions.
type nopTracer struct{}

func (nopTracer) Trace(Decision) {}

// LogTracer writes one human-readable line per operation, safe for
// concurrent use.
type LogTracer struct {
	mu sync.Mutex
	w  io.Writer
}

func NewLogTracer(w io.Writer) *LogTracer {
	if w == nil {
		w = os.Stdout
	}
	return &LogTracer{w: w}
}

func (l *LogTracer) Trace(d Decision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "t=%d op=%s in={%s} -> %s reason=%s %s\n",
		d.At, d.Op, d.Input, d.Outcome, d.Reason, d.Detail)
}

// WithTracer installs a tracer (nil disables tracing).
func (c *Coordinator) WithTracer(t Tracer) *Coordinator {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t == nil {
		t = nopTracer{}
	}
	c.tracer = t
	return c
}

func (c *Coordinator) trace(d Decision) {
	if c.tracer != nil {
		c.tracer.Trace(d)
	}
}
