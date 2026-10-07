package ontology

import (
	"errors"
	"fmt"
)

// 三类彼此互斥的拒绝原因。
var (
	ErrVersionConflict  = errors.New("version conflict: baseline is stale")
	ErrCardinality      = errors.New("cardinality constraint violated")
	ErrRetriesExhausted = errors.New("retries exhausted while contending with concurrent updates")
)

// VersionConflictError 携带调用方基线与当前版本，便于调用方决策。
type VersionConflictError struct {
	ObjectID      string
	BaseVersion   int64
	LatestVersion int64
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("version conflict on %s: baseline %d is stale (latest %d)",
		e.ObjectID, e.BaseVersion, e.LatestVersion)
}
func (e *VersionConflictError) Unwrap() error { return ErrVersionConflict }

// CardinalityError 携带本次（最新读取下）不满足的每一个约束及判定依据。
type Violation struct {
	LinkType  string
	Direction Direction
	Current   int
	Projected int
	Max       int
}

type CardinalityError struct {
	ObjectID   string
	Violations []Violation
}

func (e *CardinalityError) Error() string {
	return fmt.Sprintf("cardinality violated on %s: %d constraint(s) unsatisfied under latest read",
		e.ObjectID, len(e.Violations))
}
func (e *CardinalityError) Unwrap() error { return ErrCardinality }

// RetriesExhaustedError 携带已用尝试次数与上限。
type RetriesExhaustedError struct {
	ObjectID    string
	Attempts    int
	MaxAttempts int
}

func (e *RetriesExhaustedError) Error() string {
	return fmt.Sprintf("retries exhausted on %s: %d/%d attempts lost to concurrent updates (not a cardinality rejection)",
		e.ObjectID, e.Attempts, e.MaxAttempts)
}
func (e *RetriesExhaustedError) Unwrap() error { return ErrRetriesExhausted }
