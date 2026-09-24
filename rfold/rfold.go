// Package rfold holds the per-Key folding rules used by the batch normalizer:
// operation validation and before/after comparison that emits retract (+) /
// insert (+) change entries for a single key. It depends on no other package.
package rfold

import (
	"errors"
	"strconv"
)

// OpKind identifies an upstream operation kind.
type OpKind int

const (
	// OpUpsert makes the key exist with the given value.
	OpUpsert OpKind = iota + 1
	// OpDelete makes the key not exist; deleting a missing key is a no-op.
	OpDelete
)

// ChangeKind identifies a downstream changelog entry kind.
type ChangeKind int

const (
	// ChgRetract is "-"：the key must currently exist with exactly Val.
	ChgRetract ChangeKind = iota + 1
	// ChgInsert is "+"：the key must currently not exist.
	ChgInsert
)

// Sentinel errors; all rejections are decided via errors.Is.
var (
	ErrEmptyKey  = errors.New("rfold: empty key")
	ErrInvalidOp = errors.New("rfold: invalid operation kind")
)

// Op is one upstream Upsert/Delete operation.
type Op struct {
	Kind OpKind
	Key  string
	Val  int64 // meaningful only for Upsert
}

// Change is one downstream changelog entry.
type Change struct {
	Kind ChangeKind
	Key  string
	Val  int64
}

// String renders e.g. +(a,2) or -(b,1).
func (c Change) String() string {
	sign := byte('+')
	if c.Kind == ChgRetract {
		sign = '-'
	}
	return string(sign) + "(" + c.Key + "," + strconv.FormatInt(c.Val, 10) + ")"
}

// Validate rejects empty keys and operation kinds other than Upsert/Delete.
func Validate(op Op) error {
	if op.Key == "" {
		return ErrEmptyKey
	}
	switch op.Kind {
	case OpUpsert, OpDelete:
		return nil
	default:
		return ErrInvalidOp
	}
}

// Diff compares one key's state before and after a batch and returns its
// changes in downstream order: the retract of the old value (if any) first,
// then the insert of the new value (if any). Equal before/after states emit
// nothing, which is the minimality guarantee.
func Diff(key string, beforeVal int64, beforeOK bool, afterVal int64, afterOK bool) []Change {
	if beforeOK == afterOK && (!beforeOK || beforeVal == afterVal) {
		return nil
	}
	out := make([]Change, 0, 2)
	if beforeOK {
		out = append(out, Change{Kind: ChgRetract, Key: key, Val: beforeVal})
	}
	if afterOK {
		out = append(out, Change{Kind: ChgInsert, Key: key, Val: afterVal})
	}
	return out
}
