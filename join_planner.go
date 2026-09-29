package ontology

import (
	"errors"
	"math/big"
)

var (
	ErrEmptyQuery         = errors.New("join planner: query must contain at least one table")
	ErrTooManyTables      = errors.New("join planner: query must contain at most twelve tables")
	ErrEmptyTableName     = errors.New("join planner: table name must not be empty")
	ErrDuplicateTableName = errors.New("join planner: table names must be unique")
	ErrUnknownPredicate   = errors.New("join planner: predicate references an unknown table")
	ErrSelfPredicate      = errors.New("join planner: predicate endpoints must be different tables")
	ErrInvalidSelectivity = errors.New("join planner: selectivity must be a positive rational number no greater than one")
	ErrNegativeRowCount   = errors.New("join planner: table row count must not be negative")
)

type Table struct {
	Name     string
	RowCount int64
}

type Predicate struct {
	LeftTable   string
	RightTable  string
	Numerator   int64
	Denominator int64
}

type Query struct {
	Tables     []Table
	Predicates []Predicate
}

type Plan struct {
	Text       string
	OutputRows *big.Rat
	Cost       *big.Rat
	Partitions int64
}

func ChoosePlan(query Query) (Plan, error) {
	normalized, err := normalizeQuery(query)
	if err != nil {
		return Plan{}, err
	}

	plan, partitions := chooseNormalizedPlan(normalized)
	plan.Partitions = partitions
	return plan, nil
}
