package snapshot

import "sync"

// Decision is one logged judgment: its inputs, output and basis.
type Decision struct {
	Stage  string
	Input  string
	Output string
	Basis  string
}

// DecisionLogger collects judgments. It is safe for concurrent use.
type DecisionLogger struct {
	mu        sync.Mutex
	decisions []Decision
}

func NewDecisionLogger() *DecisionLogger {
	return &DecisionLogger{}
}

func (l *DecisionLogger) Log(stage, input, output, basis string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.decisions = append(l.decisions, Decision{stage, input, output, basis})
}

func (l *DecisionLogger) Decisions() []Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Decision, len(l.decisions))
	copy(out, l.decisions)
	return out
}
