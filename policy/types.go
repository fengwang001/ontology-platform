// Package policy holds tables, rows, row-level policies, column mask rules and
// the policy epoch, and serves immutable read snapshots under a single RWMutex.
package policy

import (
	"errors"
)

// ColType is the declared type of a column: int64 or byte string.
type ColType uint8

const (
	TypeInt ColType = iota + 1
	TypeStr
)

// Op is an atomic comparison operator.
type Op uint8

const (
	OpEq Op = iota + 1
	OpNe
	OpLt
	OpLe
)

// Kind selects how a row policy participates in visibility.
type Kind uint8

const (
	Permissive Kind = iota + 1
	Restrictive
)

// Cell is one raw value. Nil means NULL; otherwise it is int64 or string.
type Cell any

// Column describes a table column.
type Column struct {
	Name string
	Type ColType
	Def  int // default mask level, 0..4
}

// Atom is 列 op 常量. A nil Constant is invalid.
type Atom struct {
	Col      string
	Op       Op
	Constant Cell
}

// RowPolicy is one permissive/restrictive conjunction bound to a role or "*".
type RowPolicy struct {
	ID    string
	Table string
	Role  string
	Kind  Kind
	Pred  []Atom
}

// MaskRule is an explicit per-role mask level for one column.
type MaskRule struct {
	Table string
	Col   string
	Role  string
	Level int
}

// Sentinel errors distinguishing every rejection class; wrap with %w.
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrTableNotFound   = errors.New("table not found")
	ErrColumnNotFound  = errors.New("column not found")
	ErrAlreadyExists   = errors.New("already exists")
	ErrNotFound        = errors.New("not found")
	ErrColumnDenied    = errors.New("column denied")
	ErrTypeMismatch    = errors.New("type mismatch")
)
