package compaction

import "math/big"

type File struct {
	ID     uint64
	Layer  int
	MinKey []byte
	MaxKey []byte
	Bytes  uint64
}

type Config struct {
	Layers             int
	ZeroTrigger        uint64
	FirstNonZeroTarget uint64
	TargetMultiplier   uint64
}

type PlanType int

const (
	PlanRewrite PlanType = iota
	PlanMoveDown
)

func (t PlanType) String() string {
	switch t {
	case PlanRewrite:
		return "rewrite"
	case PlanMoveDown:
		return "move-down"
	default:
		return "unknown"
	}
}

type Plan struct {
	ID          uint64
	Type        PlanType
	SourceLayer int
	TargetLayer int
	InputIDs    []uint64
	MinKey      []byte
	MaxKey      []byte
}

type LayerScore struct {
	Layer int
	Score *big.Rat
}

type SkipReason int

const (
	SkipNone SkipReason = iota
	SkipNotEligible
	SkipOccupied
)

type PlanResult struct {
	Plan         *Plan
	RankedScores []LayerScore
	Skips        map[int]SkipReason
	Reason       string
}

type OperationLog interface {
	Log(operation string, input any, output any, reason string)
}
