package agg

import "math"

// Kind 标识一个聚合器。
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

// AllKinds 是全部聚合器，顺序固定。
var AllKinds = []Kind{Count, Sum, Min, Max, DistinctCount}

func (k Kind) String() string {
	return [...]string{"count", "sum", "min", "max", "distinct_count"}[k]
}

// Aggregator 描述一个聚合器的增量能力。
type Aggregator interface {
	Kind() Kind
	// InsertIncremental 插入是否可增量：全部为 true。
	InsertIncremental() bool
	// DeleteIncremental 删除是否可仅由聚合状态与被删值完成。
	DeleteIncremental() bool
	// NeedsMembersOnDelete 删除时是否需要访问该组成员（触发重算）。
	NeedsMembersOnDelete() bool
	// Empty 是无成员组的初始值。
	Empty() float64
}

type base struct{ kind Kind }

func (b base) Kind() Kind              { return b.kind }
func (b base) InsertIncremental() bool { return true }

type decrementable struct{ base }

func (d decrementable) DeleteIncremental() bool    { return true }
func (d decrementable) NeedsMembersOnDelete() bool { return false }

type memberBound struct{ base }

func (m memberBound) DeleteIncremental() bool    { return false }
func (m memberBound) NeedsMembersOnDelete() bool { return true }

// Family 返回五个聚合器。
func Family() map[Kind]Aggregator {
	return map[Kind]Aggregator{
		Count:         decrementable{base{Count}},
		Sum:           decrementable{base{Sum}},
		Min:           memberBound{base{Min}},
		Max:           memberBound{base{Max}},
		DistinctCount: memberBound{base{DistinctCount}},
	}
}

func (d decrementable) Empty() float64 {
	if d.kind == Sum {
		return 0
	}
	return 0
}
func (m memberBound) Empty() float64 { return 0 }

// Values 是一组五个聚合结果，下标为 Kind。
type Values [5]float64

// Recompute 依据成员多重集（值→出现次数）全量重算全部聚合。
// visited 每访问一条成员记录加一，因此恰等于成员总数。
func Recompute(members map[float64]int64) (Values, int64) {
	var v Values
	var total int64
	var distinct int64
	var sum float64
	min := math.Inf(1)
	max := math.Inf(-1)
	for val, n := range members {
		total += n
		distinct++
		for i := int64(0); i < n; i++ {
			sum += val
		}
		if val < min {
			min = val
		}
		if val > max {
			max = val
		}
	}
	v[Count] = float64(total)
	v[Sum] = sum
	v[DistinctCount] = float64(distinct)
	if total > 0 {
		v[Min] = min
		v[Max] = max
	}
	return v, total
}
