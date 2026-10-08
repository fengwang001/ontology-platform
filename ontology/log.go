package ontology

import (
	"sort"
	"strconv"
	"sync"
)

// Decision 记录一次判定的输入、输出与依据。
type Decision struct {
	Op     string
	Input  string
	Output string
	Basis  string
}

// DecisionLog 线程安全的判定日志。
type DecisionLog struct {
	mu      sync.Mutex
	entries []Decision
}

func NewDecisionLog() *DecisionLog {
	return &DecisionLog{}
}

func (l *DecisionLog) add(d Decision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, d)
}

// Entries 返回当前日志快照。
func (l *DecisionLog) Entries() []Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Decision, len(l.entries))
	copy(out, l.entries)
	return out
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func propKeys(props map[string]any) string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "["
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k
	}
	return out + "]"
}
