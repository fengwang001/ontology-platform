// Package audit implements the permission-decision audit, replay and
// correction subsystem.
//
// This file contains the shared domain types and the fixed error taxonomy.
package audit

import (
	"errors"
	"time"
)

// Effect is the effect of a rule.
type Effect string

const (
	EffectAllow Effect = "ALLOW"
	EffectDeny  Effect = "DENY"
)

// Rule is a single permission rule. Empty Subjects/Targets/Actions means a
// wildcard for that dimension. Rules are evaluated first-match-wins after the
// rule set is canonicalised.
type Rule struct {
	ID       string
	Subjects []string
	Targets  []string
	Actions  []string
	Effect   Effect
	Order    int
}

// RuleSet is an immutable, ordered set of rules.
type RuleSet struct {
	Rules []Rule
}

// RuleVersionInput is the caller supplied content for a new rule version.
type RuleVersionInput struct {
	// ID is the caller chosen unique version identifier. It must not exist yet.
	ID       string
	ParentID string
	Rules    RuleSet
}

// RuleVersion is one committed, immutable version of the permission rules.
//
// Versions form a single append-only hash chain: ParentID points at the
// previously committed version, Seq is the unique monotonic order and
// ChainHash binds content, parent and sequence.
type RuleVersion struct {
	ID        string
	Seq       int64
	ParentID  string
	Rules     RuleSet
	CreatedAt time.Time
	// ContentHash binds exactly the rule content (ID/Parent/Rules).
	ContentHash string
	// PrevChainHash is the previous version's ChainHash ("" for the root).
	PrevChainHash string
	// ChainHash binds ContentHash, Seq, ParentID and PrevChainHash.
	ChainHash string
}

// AuditInput is the caller supplied portion of an audit record.
//
// AccessRequest is the raw input of one access adjudication.
type AccessRequest struct {
	Subject string
	Target  string
	Action  string
	// Content is the verbatim request content as originally received.
	Content string
}

type AuditInput struct {
	Subject       string
	Target        string
	Action        string
	Content       string
	RuleVersionID string
}

// AuditRecord is one append-only record of an access decision that actually
// happened. Every field is immutable once committed; RecordHash binds the
// whole record and PrevHash links it into the audit hash chain.
type AuditRecord struct {
	ID            string
	Seq           int64
	Subject       string
	Target        string
	Action        string
	Content       string
	DecidedAt     time.Time
	Allow         bool
	RuleVersionID string
	// EngineTag identifies the adjudication implementation that produced the
	// original decision (provenance only; replay always uses the canonical
	// engine).
	EngineTag  string
	PrevHash   string
	RecordHash string
}

// CorrectionTarget identifies what a correction corrects.
type CorrectionTarget string

const (
	CorrectionTargetAudit      CorrectionTarget = "AUDIT"
	CorrectionTargetCorrection CorrectionTarget = "CORRECTION"
)

// Correction is an append-only correction record. It never modifies the
// original audit record: AuditID always points at the original record while
// TargetType/TargetID name the direct predecessor in the per-audit correction
// chain (the original record or the previous correction).
type Correction struct {
	ID             string
	Seq            int64
	AuditID        string
	TargetType     CorrectionTarget
	TargetID       string
	CorrectedAllow bool
	Reason         string
	CreatedAt      time.Time
	PrevHash       string
	RecordHash     string
}

// ReplayOutcome is the result classification of a replay.
type ReplayOutcome string

const (
	// ReplayMatch means re-adjudication reproduced the original decision.
	ReplayMatch ReplayOutcome = "MATCH"
	// ReplayMismatchDefectiveOriginal means the stored rule content is intact
	// but the re-adjudicated result differs: the original decision was
	// defective.
	ReplayMismatchDefectiveOriginal ReplayOutcome = "MISMATCH_DEFECTIVE_ORIGINAL"
	// ReplayIntegrityAuditTampered means the original audit record itself was
	// altered.
	ReplayIntegrityAuditTampered ReplayOutcome = "INTEGRITY_AUDIT_TAMPERED"
	// ReplayIntegrityVersionTampered means the rule version content bound to
	// the audit record was altered after the fact.
	ReplayIntegrityVersionTampered ReplayOutcome = "INTEGRITY_VERSION_TAMPERED"
)

// ReplayReport is the full result of a replay.
type ReplayReport struct {
	AuditID       string
	RuleVersionID string
	OriginalAllow bool
	ReplayedAllow bool
	Outcome       ReplayOutcome
	EngineTag     string
	// VersionsTouched is the number of distinct rule versions read.
	VersionsTouched int
	// VersionBytesRead is the total size of rule version material read.
	VersionBytesRead int64
	// AuditBytesRead is the size of audit material read during verification.
	AuditBytesRead int64
}

// LegalityKind is one of the three externally distinguishable answers.
type LegalityKind string

const (
	// LegalityOriginalAllowed: the original audit says allow and no active
	// correction overturns it.
	LegalityOriginalAllowed LegalityKind = "ORIGINAL_ALLOWED"
	// LegalityOriginalDenied: the original audit says deny and no active
	// correction overturns it.
	LegalityOriginalDenied LegalityKind = "ORIGINAL_DENIED"
	// LegalityCorrected: at least one not-yet-negated correction changes the
	// conclusion relative to the original record.
	LegalityCorrected LegalityKind = "CORRECTED"
)

// LegalityView answers "was subject's access at that historical point legal".
type LegalityView struct {
	AuditID          string
	Kind             LegalityKind
	OriginalAllow    bool
	EffectiveAllow   bool
	ActiveCorrection *Correction
}

// The error taxonomy has one fixed global precedence. When an operation could
// fail in several ways at once, the error with the smallest precedence number
// is the unique reported error.
//
//	1 ErrAuditNotFound
//	2 ErrVersionNotFound
//	3 ErrVersionTampered
//	4 ErrAuditTampered
//	5 ErrCorrectionTargetMissing
//	6 ErrCorrectionTampered
//	7 ErrCorrectionConflict
//	8 ErrReplayMatch (append rejected: nothing to correct)
//	9 ErrInvalidInput
//	10 ErrAlreadyExists
var (
	ErrAuditNotFound           = errors.New("audit record not found")
	ErrVersionNotFound         = errors.New("rule version not found")
	ErrVersionTampered         = errors.New("rule version integrity violated")
	ErrAuditTampered           = errors.New("audit record integrity violated")
	ErrCorrectionTargetMissing = errors.New("correction target record not found")
	ErrCorrectionTampered      = errors.New("correction record integrity violated")
	ErrCorrectionConflict      = errors.New("correction does not advance the chain conclusion")
	ErrReplayMatch             = errors.New("replay matches original decision; no correction needed")
	ErrInvalidInput            = errors.New("invalid input")
	ErrAlreadyExists           = errors.New("record already exists")
)

// ErrorPrecedence maps each sentinel error to its fixed reporting precedence.
var ErrorPrecedence = map[error]int{
	ErrAuditNotFound:           1,
	ErrVersionNotFound:         2,
	ErrVersionTampered:         3,
	ErrAuditTampered:           4,
	ErrCorrectionTargetMissing: 5,
	ErrCorrectionTampered:      6,
	ErrCorrectionConflict:      7,
	ErrReplayMatch:             8,
	ErrInvalidInput:            9,
	ErrAlreadyExists:           10,
}
