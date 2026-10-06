package refupdate

import "sync"

// RejectReason identifies why an instruction was rejected. The constants are
// ordered by the mandated precedence; adjudicate reports only the first one
// that applies.
type RejectReason int

const (
	RejectNone RejectReason = iota
	RejectMalformedInstruction
	RejectInvalidRefName
	RejectObjectMissing
	RejectOldValueMismatch
	RejectProtectedCreate
	RejectProtectedDelete
	RejectProtectedAllowlist
	RejectTagImmutable
	RejectProtectedNonFF
	RejectNonFFWithoutForce
	RejectBatchConflict
	// SkippedDueToOtherReject marks an individually-passing instruction that
	// did not execute because another instruction in the same push failed.
	SkippedDueToOtherReject
)

// String returns the canonical reason token.
func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "accepted"
	case RejectMalformedInstruction:
		return "malformed-instruction"
	case RejectInvalidRefName:
		return "invalid-ref-name"
	case RejectObjectMissing:
		return "object-missing"
	case RejectOldValueMismatch:
		return "old-value-mismatch"
	case RejectProtectedCreate:
		return "protected-no-create"
	case RejectProtectedDelete:
		return "protected-no-delete"
	case RejectProtectedAllowlist:
		return "protected-allowlist-only"
	case RejectTagImmutable:
		return "tag-immutable"
	case RejectProtectedNonFF:
		return "protected-no-non-fast-forward"
	case RejectNonFFWithoutForce:
		return "non-fast-forward-without-force"
	case RejectBatchConflict:
		return "batch-conflict"
	case SkippedDueToOtherReject:
		return "skipped-other-rejected"
	default:
		return "unknown"
	}
}

// Instruction is one reference update command inside a push.
// Empty OldID means "the reference must not yet exist" (create).
// Empty NewID means delete. Both empty is parameter-illegal.
type Instruction struct {
	Ref   RefName
	OldID CommitID
	NewID CommitID
	Force bool
}

// ItemResult is the verdict for one instruction.
type ItemResult struct {
	Index  int
	Reason RejectReason
	// Executed is false for rejected and for co-skipped instructions.
	Executed bool
	Before   CommitID
	After    CommitID
}

// AuditEntry records one fully-committed push with every before/after value.
type AuditEntry struct {
	Seq   int
	User  string
	Items []AuditItem
}

// AuditItem is one executed instruction inside an audit record.
type AuditItem struct {
	Ref    RefName
	Before CommitID
	After  CommitID
	Force  bool
}

// PushReport is the complete response for one push.
type PushReport struct {
	Accepted bool
	Items    []ItemResult
	// AuditSeq is valid only when Accepted.
	AuditSeq int
}

// Store is the concurrency-safe reference database. A single mutex serializes
// pushes, giving strict serializability: each push is atomic with respect to
// other pushes and to readers, so no reader can observe a partially applied
// batch and concurrent pushes against the same old value cannot both succeed.
type Store struct {
	mu      sync.Mutex
	refs    map[RefName]CommitID
	graph   *Graph
	rules   *RuleSet
	nextSeq int
	audit   []AuditEntry
}

// NewStore creates a store backed by graph.
func NewStore(graph *Graph) *Store {
	return &Store{
		refs:  make(map[RefName]CommitID),
		graph: graph,
		rules: mustRuleSet(nil),
	}
}

func mustRuleSet(rules []Rule) *RuleSet {
	rs, err := NewRuleSet(rules)
	if err != nil {
		panic(err)
	}
	return rs
}

// SetRules atomically replaces the protection rules. A push reads the rules
// while holding the push lock, so an in-flight adjudication always sees one
// consistent snapshot and a concurrent SetRules applies to a later push.
func (s *Store) SetRules(rules []Rule) error {
	rs, err := NewRuleSet(rules)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rules = rs
	s.mu.Unlock()
	return nil
}

// Lookup returns the current target of a reference (empty when absent).
func (s *Store) Lookup(name RefName) CommitID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refs[name]
}

// Refs returns a point-in-time snapshot of all references.
func (s *Store) Refs() map[RefName]CommitID {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[RefName]CommitID, len(s.refs))
	for k, v := range s.refs {
		out[k] = v
	}
	return out
}

// AuditLog returns a snapshot copy of all committed audit records.
func (s *Store) AuditLog() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.audit))
	for i, e := range s.audit {
		items := make([]AuditItem, len(e.Items))
		copy(items, e.Items)
		out[i] = AuditEntry{Seq: e.Seq, User: e.User, Items: items}
	}
	return out
}
