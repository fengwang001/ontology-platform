package joinorder

import "math/big"

type Table struct {
	Name string
	Rows int64
}

type Predicate struct {
	LeftTable   string
	RightTable  string
	Numerator   int64
	Denominator int64
}

type Plan struct {
	Text string
	Rows *big.Rat
	Cost *big.Rat
}

type Result struct {
	Plan               Plan
	PartitionsExamined int64
}

type Selector struct{}

func NewSelector() *Selector {
	return &Selector{}
}
