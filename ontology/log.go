package ontology

import (
	"encoding/json"
	"io"
	"sync"
)

// RuleBasis identifies one rule that matched a (subject, instance)
// pair and the conclusion it contributed.
type RuleBasis struct {
	RuleID     string `json:"rule_id"`
	Property   string `json:"property,omitempty"`
	Effect     string `json:"effect"`
	Presented  string `json:"presented,omitempty"` // raw|masked
	PredicateN int    `json:"predicates_matched,omitempty"`
}

// DecisionTrace is the complete, order-independent policy basis of one
// verdict. Rule IDs are sorted, and all counts are exact.
type DecisionTrace struct {
	RowMode          string      `json:"row_mode,omitempty"`
	PropMode         string      `json:"prop_mode,omitempty"`
	MatchedRowRules  []RuleBasis `json:"matched_row_rules,omitempty"`
	MatchedPropRules []RuleBasis `json:"matched_prop_rules,omitempty"`
	RowOutcome       string      `json:"row_outcome"` // allow|deny|default_deny
	// Evaluated is the number of policy rules whose body actually had
	// to be evaluated for THIS call (after candidate selection). It is
	// the observable complexity evidence: bounded by the number of
	// rules that can possibly hit the instance/subject, independent of
	// total registered policies or total instance count.
	RowRulesEvaluated  int `json:"row_rules_evaluated"`
	PropRulesEvaluated int `json:"prop_rules_evaluated"`
}

// CallRecord is one fully logged adjudication call: inputs, output and
// the policy basis. Concrete output is serialized into OutputJSON.
type CallRecord struct {
	Seq        int64           `json:"seq"`
	Kind       string          `json:"kind"` // read|write
	SubjectID  string          `json:"subject_id"`
	InstanceID string          `json:"instance_id"`
	InputJSON  json.RawMessage `json:"input"`
	OutputJSON json.RawMessage `json:"output"`
	ErrorKind  string          `json:"error_kind,omitempty"`
	ErrorMsg   string          `json:"error_msg,omitempty"`
	Trace      DecisionTrace   `json:"trace"`
}

// Logger receives one record per adjudication call, synchronously
// with the call. Implementations must be safe for concurrent use.
type Logger interface {
	Log(rec CallRecord)
}

// SliceLogger keeps every record in memory; safe for concurrent use.
type SliceLogger struct {
	mu      sync.Mutex
	Records []CallRecord
}

func (l *SliceLogger) Log(rec CallRecord) {
	l.mu.Lock()
	l.Records = append(l.Records, rec)
	l.mu.Unlock()
}

// Snapshot returns a defensive copy of all records so far.
func (l *SliceLogger) Snapshot() []CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]CallRecord, len(l.Records))
	copy(cp, l.Records)
	return cp
}

// JSONLogger writes one JSON object per line; safe for concurrent use.
type JSONLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewJSONLogger(w io.Writer) *JSONLogger { return &JSONLogger{w: w} }

func (l *JSONLogger) Log(rec CallRecord) {
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	_, _ = l.w.Write(b)
	_, _ = l.w.Write([]byte{'\n'})
	l.mu.Unlock()
}

// MultiLogger fans a record out to several loggers.
type MultiLogger []Logger

func (m MultiLogger) Log(rec CallRecord) {
	for _, l := range m {
		l.Log(rec)
	}
}
