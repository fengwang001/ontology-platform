package ontology

// tier 为手续费档位：0 最宽松（远），1 中档，2 最严（近）。
type tier int

const (
	tierFar tier = iota
	tierMid
	tierNear
)

type tierResult struct {
	tier     tier
	departed bool // 操作时刻不小于出发时刻
}

// tierFor 按 delta = 出发时刻 - 操作时刻 划分档位。
// delta == 阈值时归入较宽松一档。
func tierFor(delta int64, longThreshold, shortThreshold int64) tierResult {
	if delta <= 0 {
		return tierResult{tier: tierNear, departed: true}
	}
	switch {
	case delta >= longThreshold:
		return tierResult{tier: tierFar}
	case delta >= shortThreshold:
		return tierResult{tier: tierMid}
	default:
		return tierResult{tier: tierNear}
	}
}

// feeAt 计算 fare * percent / 100 并向上取整到分。percent ∈ [0,100]。
func feeAt(fare int64, percent int) int64 {
	if percent <= 0 || fare <= 0 {
		return 0
	}
	p := int64(percent)
	return (fare*p + 99) / 100
}

func percentAt(p [3]int, t tier) int { return p[t] }
