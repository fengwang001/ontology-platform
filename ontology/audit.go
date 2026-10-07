package ontology

// AuditEvent records one engine call: its input, its final output (or
// error), and — for access decisions — the tag and role evidence the
// decision was based on.
type AuditEvent struct {
	Op     string `json:"op"`
	Input  any    `json:"input,omitempty"`
	Output any    `json:"output,omitempty"`
	Err    string `json:"err,omitempty"`
}

// AuditLogger receives one event per engine call. Implementations must be
// safe for concurrent use; the engine invokes Log while holding its lock,
// so events observe a serial order.
type AuditLogger interface {
	Log(ev AuditEvent)
}

// NopLogger discards all events.
type NopLogger struct{}

// Log implements AuditLogger.
func (NopLogger) Log(AuditEvent) {}

// MemoryLogger keeps events in memory. It is safe for concurrent use.
type MemoryLogger struct {
	ch chan AuditEvent
}

// NewMemoryLogger creates a logger with the given buffer capacity.
func NewMemoryLogger(capacity int) *MemoryLogger {
	if capacity < 1 {
		capacity = 1
	}
	return &MemoryLogger{ch: make(chan AuditEvent, capacity)}
}

// Log implements AuditLogger. It drops events when the buffer is full so a
// slow consumer can never block the engine.
func (m *MemoryLogger) Log(ev AuditEvent) {
	select {
	case m.ch <- ev:
	default:
	}
}

// Events drains and returns all buffered events in arrival order.
func (m *MemoryLogger) Events() []AuditEvent {
	var out []AuditEvent
	for {
		select {
		case ev := <-m.ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}
