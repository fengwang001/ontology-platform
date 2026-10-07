package ontology

import "sync"

// Decision 是一次占用申请或更新尝试的完整判定记录，足以重放核验。
type Decision struct {
	Seq         int64               `json:"seq"`
	At          int64               `json:"at"`
	Kind        string              `json:"kind"`
	Caller      string              `json:"caller"`
	Keys        []string            `json:"keys,omitempty"`
	BaseVer     int64               `json:"base_version,omitempty"`
	TTL         int64               `json:"ttl_ms,omitempty"`
	Token       int64               `json:"token,omitempty"`
	State       string              `json:"state,omitempty"`
	Patch       Props               `json:"patch,omitempty"`
	Links       map[string][]string `json:"links,omitempty"`
	Outcome     Outcome             `json:"outcome"`
	Reason      string              `json:"reason"`
	Holder      string              `json:"holder,omitempty"`
	ConflictKey string              `json:"conflict_key,omitempty"`
	NewVersion  int64               `json:"new_version,omitempty"`
}

// DecisionLog 是只追加的判定日志。
type DecisionLog struct {
	mu      sync.Mutex
	entries []Decision
}

// NewDecisionLog 创建空日志。
func NewDecisionLog() *DecisionLog { return &DecisionLog{} }

func (l *DecisionLog) appendLocked(d Decision) Decision {
	l.entries = append(l.entries, d)
	return d
}

// Entries 返回全部判定记录的副本，按判定顺序排列。
func (l *DecisionLog) Entries() []Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Decision(nil), l.entries...)
}
