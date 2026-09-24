// Package confidence 在部分分片失败时判定五种聚合的可信度并给出区间估计。
package confidence

import (
	"fmt"

	"ontology/combine"
)

// Kind 是可信度类别。
type Kind int

const (
	Exact      Kind = iota // 全部成功，结果精确
	LowerBound             // 当前值是真实值的下界（Count/Sum/Max）
	UpperBound             // 当前值是真实值的上界（Min）
	Prefix                 // TopK：仅前 TrustedPrefix 项的入选确定
	Unknown                // 无成功分片，无可言
)

// Assessment 是一种聚合的可信度判定与区间估计。
type Assessment struct {
	Kind          Kind
	Note          string
	TrustedPrefix int // 仅 TopK 使用
	Lo, Hi        float64
	LoInf, HiInf  bool
}

// Input 是判定所需的事实：分片成败、缺失上界之和与合并值。
type Input struct {
	Total, OK    int
	MissingBound float64 // 所有缺失分片的上界之和 B
	Value        float64 // 合并出的当前值
	TopK         []combine.TopEntry
}

func exact(v float64) Assessment {
	return Assessment{Kind: Exact, Note: "精确", Lo: v, Hi: v}
}

func partial(in Input) bool { return in.OK < in.Total }

// AssessCount 判定 Count：缺失只会漏记，当前值是下界。
func AssessCount(in Input) Assessment {
	if !partial(in) {
		return exact(in.Value)
	}
	return Assessment{Kind: LowerBound, Note: fmt.Sprintf("≥ %v（缺失分片只增不减）", in.Value),
		Lo: in.Value, HiInf: true}
}

// AssessSum 判定 Sum：与 Count 同理，当前值是下界。
func AssessSum(in Input) Assessment {
	if !partial(in) {
		return exact(in.Value)
	}
	return Assessment{Kind: LowerBound, Note: fmt.Sprintf("≥ %v（缺失分片只增不减）", in.Value),
		Lo: in.Value, HiInf: true}
}

// AssessMin 判定 Min：缺失分片可能含更小值，当前值是真实 Min 的上界。
func AssessMin(in Input) Assessment {
	if !partial(in) {
		return exact(in.Value)
	}
	return Assessment{Kind: UpperBound, Note: fmt.Sprintf("真实 Min ≤ %v", in.Value),
		Lo: 0, Hi: in.Value}
}

// AssessMax 判定 Max：缺失分片可能含更大值，当前值是真实 Max 的下界。
func AssessMax(in Input) Assessment {
	if !partial(in) {
		return exact(in.Value)
	}
	return Assessment{Kind: LowerBound, Note: fmt.Sprintf("真实 Max ≥ %v", in.Value),
		Lo: in.Value, HiInf: true}
}

// AssessTopK 判定 TopK：得分严格大于缺失上界之和 B 的头部条目入选确定
// （排名不确定），它们构成可信前缀；全部成功时整个榜单精确。
func AssessTopK(in Input) Assessment {
	if !partial(in) {
		return Assessment{Kind: Exact, Note: "精确", TrustedPrefix: len(in.TopK)}
	}
	prefix := 0
	for prefix < len(in.TopK) && in.TopK[prefix].Score > in.MissingBound {
		prefix++
	}
	return Assessment{Kind: Prefix, TrustedPrefix: prefix,
		Note: fmt.Sprintf("可信前缀 %d/%d（缺失上界之和 B=%v）", prefix, len(in.TopK), in.MissingBound)}
}
