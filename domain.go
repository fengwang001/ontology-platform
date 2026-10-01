package ontology

import "errors"

type PredicateKind int

const (
	KindCmp PredicateKind = iota + 1
	KindEq
	KindNe
)

type CmpOp string

const (
	OpEq CmpOp = "="
	OpNe CmpOp = "!="
	OpLt CmpOp = "<"
	OpLe CmpOp = "<="
	OpGt CmpOp = ">"
	OpGe CmpOp = ">="
)

type Predicate struct {
	Kind    PredicateKind
	Column  string
	Column2 string
	Op      CmpOp
	Value   int64
}

func Cmp(column string, op CmpOp, value int64) Predicate {
	return Predicate{Kind: KindCmp, Column: column, Op: op, Value: value}
}

func Eq(left, right string) Predicate {
	return Predicate{Kind: KindEq, Column: left, Column2: right}
}

func Ne(left, right string) Predicate {
	return Predicate{KindNe, left, right, "", 0}
}

var (
	ErrInvalidPredicate = errors.New("invalid predicate")
	ErrContradiction    = errors.New("predicate contradicts existing constraints")
	ErrColumnNotFound   = errors.New("column not registered")
)
